# 发现与决策

## 需求
- 以代码审查方式分析 `internal/agent`，而不是直接进入实现。
- 优先关注明确的 bug、风险、行为回归和缺失的保护措施。
- 在最终回复中提供带精确文件和行号的结论。
- 在确认“群聊主场景 + 已有异步回推基础”后，正式落地问题 3 的群聊编排重构。

## 调研发现
- `internal/agent` 目前包含 `agent.go`、`client.go`、`port.go`、`ratelimit.go`、`session.go` 以及 `tools/` 子包。
- 仓库流程要求在 `tasks/todo.md` 中维护当前任务，并在宣称完成前给出验证证据。
- `agent.go` 负责驱动一个最多 8 轮的 ReAct 循环，带有会话历史、按会话限流和基于角色的工具暴露。
- `client.go` 使用 OpenAI 兼容的聊天接口，内置 3 次重试，并在工具调用标记泄漏到文本中时做 DSML 兜底解析。
- `session.go` 只保留每个会话最近 20 条非 system 消息，并依靠周期性清理来做 TTL 过期处理。
- `port.go` 只是类型别名封装，没有额外行为。
- `ratelimit.go` 实现了一个固定 1 分钟窗口、最多 10 次请求的限流器。
- `tools/registry.go` 把工具定义保存在内存中，先按角色过滤暴露，再在分发时二次检查权限。
- `tools/schedule.go` 注册了 4 个只读工具，并且对工具参数的 JSON 反序列化错误采取了静默忽略。
- 工具层默认所有注入的 port 都非空，注册和分发时没有额外防御性检查。
- `tools/attendance.go` 会把名单格式化成带编号的字符串，供 LLM 直接引用，同样忽略 JSON 反序列化失败。
- `tools/analytics.go` 暴露了较灵活的统计和交叉查询能力，但在把参数传给后端 port 之前也没有额外校验。
- `tools/admin.go` 里只有一个处理器（`sign_for_user`）显式检查了 JSON 解析错误，其余处理器在错误参数下仍可能继续执行并得到零值。
- 在 `agent.go` 中，如果最后一条消息是工具结果，下一轮 LLM 请求曾经会省略 `tools`；再结合 `client.go` 的 `omitempty`，标准的后续工具调用在第一轮后会被切断。
- 在 `tools/admin.go` 中，`subscribe_attendance_push` 曾忽略 JSON 解析错误；由于适配层把空 `deptIDs` 解释为“全部部门”，坏参数可能把一个有范围的订阅放大成全租户订阅。
- 在 `agent.go` 中，LLM 请求是从 `context.Background()` 派生的，而不是沿用传入请求的上下文，因此上游取消和停机信号无法停止仍在执行的 50 秒/90 秒 LLM 调用。
- 在 `tools/schedule.go` 中，`get_current_time` 的 `weekday_num` 曾直接使用 Go 的 `time.Weekday` 编号（周日为 0），而其他 agent schema 约定星期编号是 1-7。
- `query_free_users_by_slot` 和 `query_rest_days` 都用 1-7 表达星期，因此 `get_current_time` 在协议上是一个明显异类。
- 针对这个问题的最小安全修复方式，是用一个单独的辅助函数把周日从 0 规范为 7，同时保持周一到周六仍为 1-6。
- 用户已经确认：钉钉传入的上游 `ctx` 超时通常比 LLM 响应时间更短。
- 用户在设计取向上更偏向折中方案，希望同时兼顾资源控制和回答完成率。
- 用户补充说明：真实使用中以群聊为主，私聊场景很少。
- 用户关注点已转向：如果采用“编排器版”方案，是否会影响现有功能或让已经实现的异步回推失效。
- 重新检查 `pkg/dingtalk/stream.go` 后确认：群聊链路已经具备异步处理、`SessionWebhook` 回复和主动推送 fallback，之前“没有异步补发链路”的判断只适用于私聊，不适用于群聊主路径。
- 当前群聊异步逻辑的主要问题不是功能缺失，而是并发控制、慢查询提示、Webhook 回复和 fallback 策略全部内联在 `handleGroupChatAsync` 中，职责过于集中，不利于后续维护。
- 采用“编排器版”时，只要保持发送顺序、超时策略和 fallback 入口不变，就不会天然影响现有功能，也不会让已实现的异步回推失效。
- 已新增 `pkg/dingtalk/group_chat_reply_orchestrator_test.go`，用测试锁定了三条核心行为：Webhook 成功、Webhook 失效 fallback、并发已满繁忙提示。
- 已新增 `pkg/dingtalk/group_chat_reply_orchestrator.go`，把群聊异步编排收束到独立对象中；`pkg/dingtalk/stream.go` 现在只负责组装依赖并委派。

## 技术决策
| 决定 | 原因 |
|------|------|
| 先读顶层 `agent` 包，再读 `tools` 子包 | 这样能先建立主执行链路，再理解工具层行为 |
| 用简洁的证据笔记记录发现，再做综合判断 | 能保证最终结论建立在具体代码路径上 |
| 后续轮次继续发送按角色过滤后的工具定义 | 这是恢复标准工具调用语义的最小改动 |
| 在产生副作用前拒绝错误的 `subscribe_attendance_push` 参数 | 这样能避免坏请求把订阅范围放大 |
| 用一个小辅助函数把周日从 0 规范到 7 | 能显式表达协议并方便测试，同时不改变响应结构 |
| 把问题 3 视为架构取舍而非自动修复的 bug | 是否应继续执行超时后的 LLM 调用，取决于结果之后是否还有机会送达用户 |
| 正式设计应围绕群聊异步回推主路径展开 | 群聊是主要流量来源，而且当前已经具备异步处理和主动推送兜底基础 |
| 方案二应优先保持现有发送链路与外部行为不变，只重组内部编排职责 | 这样才能在提高清晰度的同时避免影响现有可用功能 |
| 让 `handleGroupChatAsync` 只保留接入层职责，具体编排迁移到独立 orchestrator | 这样能把“接消息”和“如何完成异步回复”两种职责拆开 |
| 用三条最关键的行为测试锁定本次重构边界 | 可以直接验证没有改坏既有群聊体验 |

## 遇到的问题
| 问题 | 处理方式 |
|------|----------|
| 在当前环境下，`tasks/` 目录中的文件有时会触发 `apply_patch` 刷新失败 | 必要时改用更小粒度的补丁或其他写入方式 |

## 参考资源
- `G:\gofile\schedule_server\AGENTS.md`
- `G:\gofile\schedule_server\tasks\todo.md`
- `G:\gofile\schedule_server\internal\agent`
- `G:\gofile\schedule_server\pkg\dingtalk\stream.go`
- `G:\gofile\schedule_server\pkg\dingtalk\group_chat_reply_orchestrator.go`

## 可视化/浏览结果
- 本次审查未使用浏览器或图片查看。

---
*每进行两次 view / browser / search 操作后就更新本文件*
*这样可以避免视觉内容在上下文中丢失*
