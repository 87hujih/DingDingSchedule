# 发现与决策

## 2026-03-23：README 补全与项目总览整理

### 需求
- 用户要求基于当前 GitHub 仓库的真实实现，整体分析项目并生成缺失的 `README.md`。
- README 需要能承担仓库首页说明作用，而不是只面向本地开发者的零散笔记。

### 调研发现
- 服务入口为 `cmd/main.go -> inits.Init() -> app.RunServer()`；进程启动时会初始化配置、日志、MySQL、AutoMigrate、GORM 租户隔离插件，并在启用时同时启动钉钉 Stream 客户端、AI Agent 和考勤调度器。
- HTTP API 统一挂在 `/api` 下，当前已实现认证、用户、部门、课表、考勤、学期、作息设置、休息日和审计日志模块；`/health` 用于容器与部署健康检查。
- 数据访问层通过 `internal/repository/tenant_gorm_plugin.go` 自动对带 `tenant_id` 的模型注入查询/写入隔离，当前自动迁移包含租户、用户、部门、课表、请假、考勤快照、人工补签覆盖、群订阅、Agent 调用日志等 17 张表。
- AI 能力不是独立 HTTP 接口，而是通过钉钉 Stream 消息接入；`internal/agent` 当前注册了课表查询、考勤查询、考勤文本生成、周排行、休息日/请假查询、群订阅管理、补签、统计分析和交叉分析等工具。
- 配置加载依赖 `CONFIG_ENV` 与可选的 `CONFIG_PATH`；默认读取 `./configs/<env>.yaml`，Docker 和生产 compose 都通过挂载 `/app/configs` 提供配置。
- 本地开发入口以 `make run` / `go run ./cmd/main.go` 为主，容器化入口使用 `docker-compose.yml`；正式发布走 `.github/workflows/ci.yml` + `.github/workflows/deploy.yml` + GHCR + `deploy.sh` + `docker-compose.prod.yml`，应急路径仍保留 `one-click-deploy.sh` 源码包部署。
- 仓库当前缺少根目录 `README.md`，但已经存在较完整的代码、部署脚本、配置文件和专项文档，可作为事实来源。
- `tasks/todo.md` 已沉淀大量近期工作记录，说明 README 需要反映项目目前已经具备的 AI、考勤、课表和部署能力，而不是停留在早期形态。

### 技术决策
| 决定 | 原因 |
|------|------|
| 先从源码与脚本提炼事实，再写 README | 避免文档继续复制历史漂移信息 |
| README 采用“项目定位 -> 核心能力 -> 技术栈 -> 运行与部署 -> 目录结构”的结构 | 适合 GitHub 首页阅读节奏 |

### 参考资源
- `G:\gofile\schedule_server\AGENTS.md`
- `G:\gofile\schedule_server\tasks\todo.md`
- `G:\gofile\schedule_server\cmd`
- `G:\gofile\schedule_server\internal`
- `G:\gofile\schedule_server\configs`
- `G:\gofile\schedule_server\Makefile`
- `G:\gofile\schedule_server\.github\workflows`
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

## 2026-03-19：CI/CD 与部署标准化规划

### 需求
- 用户当前通过 `ONE_CLICK_DEPLOY_GUIDE.md` 和 `one-click-deploy.sh` 做日常发布，希望知道对于更规范的项目部署流程，需要做哪些改变。
- 用户确认需要把建议进一步落成仓库内实施计划，而不是停留在口头建议。

### 调研发现
- 当前仓库存在且仅存在一条 GitHub Actions 工作流 `.github/workflows/deploy.yml`，它更接近“自动部署”而不是完整 CI/CD，因为缺少 PR / 普通 push 级别的测试和 lint。
- 当前生产发布主路径仍是 `ONE_CLICK_DEPLOY_GUIDE.md -> one-click-deploy.sh -> pack-for-deploy.sh -> deploy.sh`，本质是“本地打包源码 -> 上传服务器 -> 服务器重新构建”。
- `deploy.yml` 和一键部署脚本之间存在目录、配置和执行方式不一致：前者使用 `/opt/schedule_server` 且上传 `configs/dev.yaml`，后者使用 `/workspace/schedule_server`，而打包脚本只复制 `configs/prod.yaml`。
- `go test ./...` 当前会在 `scripts` 包失败，因为多个脚本文件共享同一个包并重复声明 `main`，这是引入标准 CI 之前必须先清掉的结构性阻塞。
- 对当前项目而言，最合适的目标状态是继续维持“单机 Docker + GitHub Actions + SSH 到单台生产机”的轻量方案，而不是引入 Kubernetes 或额外发布平台。

### 技术决策
| 决定 | 原因 |
|------|------|
| 将改造拆为 6 个任务 | 这样每个阶段都能独立验证，不会把 CI、CD、文档和服务器约定揉成一次大重构 |
| 优先处理 `scripts` 目录结构 | 先恢复 `go test ./...`，CI 才有可靠入口 |
| 为生产环境新增独立的 `docker-compose.prod.yml` | 将本地运行与生产部署解耦，减少配置漂移 |
| 让 `deploy.sh` 从“build-and-run”转为“pull-and-run” | 让服务器只消费制品，去掉现场构建 |
| 将正式生产文档与应急文档分离 | 正式流程和救火流程的目标不同，不应继续混写在一份 one-click 文档里 |

### 参考资源
- `G:\gofile\schedule_server\ONE_CLICK_DEPLOY_GUIDE.md`
- `G:\gofile\schedule_server\one-click-deploy.sh`
- `G:\gofile\schedule_server\deploy.sh`
- `G:\gofile\schedule_server\.github\workflows\deploy.yml`
- `G:\gofile\schedule_server\docs\superpowers\plans\2026-03-19-cicd-deployment-standardization-plan.md`
