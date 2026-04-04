# Agent Unified Planner Refactor Design

## 背景

当前 `internal/agent` 经过多轮演进后，已经同时具备：

- 基于工具注册表的 ReAct 执行链路
- `help / action / live-query / rule / mixed / clarify` 分流
- 轻量 RAG 检索
- 会话级 `active_task`

但真实体验说明，系统已经出现新的结构性问题：

1. 用户发出业务内短句或跟进回复时，仍会莫名其妙落到站外拒答
2. 用户表达不标准时，RAG 很多时候根本没有机会参与
3. 同一条消息会被 `conversation interpreter`、`domainGate`、`taskRouter`、`queryRouter` 多层抢答
4. `outOfDomainReply`、`noKnowledgeReply`、澄清追问之间的边界并不稳定

问题已经不再是“某几个关键词不够”，而是主决策链顺序错误。

## 问题定义

### 问题 1：领域门禁拥有过强的前置裁决权

当前 `Agent.chat` 在会话层判断之后，仍会立刻执行 `domainGate.Check(msg.Content)`。

这意味着：

- 只要当前这一句不命中门禁关键词，就会直接拒答
- 历史上下文、RAG 证据、任务候选都没有出手机会
- 领域门禁实际上在决定“是否允许系统继续理解”

这和“门禁只是一个 hint”完全不是一回事。

### 问题 2：RAG 不是证据层，而是条件性能力

当前只有当 `initialDecision.Intent` 被先判成 `rule` 或 `mixed` 时，才会调用知识检索。

这导致：

- 很多业务内消息在检索前就被截断
- RAG 无法帮助判断“这是不是业务内问题”
- RAG 也无法帮助澄清“这是规则说明还是实时查询”

结果就是“系统明明有知识，但消息根本没进检索”。

### 问题 3：主流程存在多个并列裁决器

当前系统里至少有四个模块会直接影响主路径：

- `conversation_interpreter`
- `domainGate`
- `taskRouter`
- `queryRouter`

这些模块不是按单一职责串起来的，而是并列抢答。

后果是：

- 同一条消息会被多套规则重复解释
- 很难预测哪一层先拦截
- 后续补丁会继续放大复杂度

### 问题 4：拒答策略过早且过重

当前系统会把下列不同情况过早地压成拒答：

- 真正站外的问题
- 业务内但表达弱的问题
- 跟进消息但没补槽成功
- 规则问法但知识弱命中
- 会话切换中的模糊消息

这会让用户感知为“Agent 完全不理解上下文”。

## 目标

本轮重构目标不是继续补关键词，而是重排主决策链，让 Agent 重新变成“先理解，再决定怎么答”。

具体目标：

- 非明显站外消息默认允许继续理解，不直接拒答
- RAG 前置为 planner 的证据层，而不是被允许后才执行
- 统一 `task / tool / rag / mixed / clarify / out_of_domain` 的决策入口
- 让 `active_task` 真正优先于单轮文本分类
- 将拒答收敛到“明显站外”这一种情况
- 保留现有工具系统、会话状态和检索实现，尽量减少 blast radius

## 非目标

- 不引入 LLM 驱动 planner
- 不改造工具注册与权限模型
- 不重写知识检索实现本身
- 不扩成开放闲聊机器人
- 不在首版做所有任务类型的全量状态化

## 方案对比

### 方案 A：继续修补现有路由链

做法：

- 保留 `conversation_interpreter -> domainGate -> taskRouter -> queryRouter -> retrieval`
- 把 `domainGate` 改成更宽松
- 给更多短句、知识弱命中、模糊请求加特判

优点：

- 局部改动小
- 上线风险相对低

缺点：

- 多层裁决器并列的问题不变
- 后续仍会反复出现“补一个场景、漏另一个场景”
- 结构上依旧是“先裁决，再理解”

### 方案 B：统一 planner 重构

做法：

- 引入单一 planner，统一输出：
  `obvious_out / continue_task / clarify / tool / rag / mixed`
- `domainGate` 降级成 `domain hint`
- 检索前置成 `retrieval prepass`
- `taskRouter`、`queryRouter`、signal heuristics 改成 planner helper

优点：

- 结构清晰
- 决策路径单一、可测
- 能从根本上解决“RAG 被饿死”和“机械拒答”

缺点：

- 需要重写 `Agent.chat` 主编排
- 要重整现有测试断言

### 方案 C：LLM 驱动统一规划

做法：

- 每轮先让 LLM 输出意图、领域、下一步动作
- 本地规则只做权限和工具执行

优点：

- 表面上最灵活

缺点：

- 不稳定
- 可测试性差
- 极易出现漂移和权限边界问题

## 推荐方案

选择方案 B。

原因：

- 当前问题已经是结构性问题，不适合继续补 patch
- 项目需要的是“可预测的任务型助手”，不是“更聪明但更漂”的自由对话系统
- 现有 `active_task`、工具注册、知识检索都还能复用，适合在此基础上收口统一 planner

## 新架构

新的主链路调整为：

`session state -> conversation interpretation -> obvious-out check -> retrieval prepass -> task candidate detection -> unified planner -> execute/clarify/reply`

### 阶段 1：会话状态优先

先读：

- `history`
- `active_task`
- 当前消息

如果当前消息明显在继续已有任务，则不允许站外拒答优先生效。

### 阶段 2：明显站外检查

将原有 `domainGate` 改成更保守的 `domain hint`：

- `obvious_out`
- `likely_in`
- `unknown`

只有 `obvious_out` 才允许最终落到站外拒答。

### 阶段 3：检索预判

对所有 `not obvious_out` 的新请求执行轻量 retrieval prepass。

检索结果作为 planner 的证据，而不是最终答案。

它主要帮助回答：

- 这条消息是不是业务内
- 这更像规则知识还是实时查询
- 当前知识命中强度是 `none / weak / strong`

### 阶段 4：任务候选识别

保留现有任务候选识别，但它不再直接决定执行。

它只负责给 planner 提供候选，例如：

- `subscribe_attendance_push`
- `sign_for_user`
- `query_subscription_status`

### 阶段 5：统一 planner 决策

统一 planner 是唯一拥有主流程裁决权的组件。

它综合：

- 会话事件
- 活动任务
- 领域 hint
- 检索结果
- 任务候选
- live / rule / action / help 等 signal

最终只输出一个 `PlanDecision`。

## Planner 结构

### 输入

```go
type PlanInput struct {
    Question          string
    UserContext       *tools.UserContext
    History           []tools.Message
    ActiveTask        *ActiveTask
    ConversationEvent conversationDecision
    DomainHint        DomainHint
    Retrieval         RetrievalResult
    TaskCandidate     *ActiveTask
    HasLiveSignal     bool
    HasRuleSignal     bool
    HasActionIntent   bool
    HasClarifyIntent  bool
    HasHelpIntent     bool
}
```

### 输出

```go
type PlanKind string

const (
    PlanObviousOut   PlanKind = "obvious_out"
    PlanContinueTask PlanKind = "continue_task"
    PlanClarify      PlanKind = "clarify"
    PlanTool         PlanKind = "tool"
    PlanRAG          PlanKind = "rag"
    PlanMixed        PlanKind = "mixed"
)

type PlanDecision struct {
    Kind              PlanKind
    ActiveTask        *ActiveTask
    ClarifyReason     string
    ToolsAllowed      []string
    FollowUpMatched   []string
    KnowledgeStrength string
}
```

## Planner 决策顺序

统一 planner 的优先级如下：

1. `greeting`
2. `cancel + active_task`
3. `task_follow_up + active_task`
4. `domain_hint == obvious_out`
5. `task_candidate != nil`
6. `retrieval strong && hasLiveSignal`
7. `retrieval strong && !hasLiveSignal`
8. `hasLiveSignal || hasActionIntent`
9. 其他情况统一 `clarify`

对应策略：

- 跟进消息补槽失败时：`clarify`
- 业务内弱表达但知识弱命中：`clarify`
- 强知识命中且无实时信号：`rag`
- 强知识命中且有实时信号：`mixed`
- 只有明显站外：`obvious_out`

## 文件边界

### 保留并降级为 helper 的文件

- `internal/agent/domain_gate.go`
  - 从最终门禁改成 `domain hint`
- `internal/agent/query_router.go`
  - 拆掉主裁决 API，只保留 signal helper
- `internal/agent/retrieval.go`
  - 保留检索实现，改入口时机
- `internal/agent/task_router.go`
  - 改成任务候选识别器
- `internal/agent/slot_filler.go`
  - 继续负责槽位补全
- `internal/agent/conversation_interpreter.go`
  - 继续负责会话事件解释

### 新增文件

- `internal/agent/planner.go`
  - 统一 planner 决策
- `internal/agent/planner_types.go`
  - planner 输入输出、hint、clarify reason
- `internal/agent/planner_test.go`
  - 统一 planner 的纯决策单测

### 主编排文件

- `internal/agent/agent.go`
  - 重写为：
    `load state -> gather evidence -> run planner -> execute -> persist`

## 回复策略

拒答策略需要重排：

- `obvious_out`
  - 允许站外拒答
- `clarify`
  - 默认回复澄清，不允许拒答
- `weak_knowledge_match`
  - 回复“我还没确认你的具体意思”
- `ambiguous_follow_up`
  - 回复“我没理解你是在补充哪个信息”

换句话说：

“不知道怎么继续”不等于“站外”。

## 日志与可观测性

现有调用日志已经有会话事件和任务状态，本轮继续扩展 planner 证据层字段：

- `domain_hint`
- `plan_kind`
- `knowledge_strength`
- `planner_reason`

这样线上排查时能直接看到：

- 是被判成明显站外
- 还是知识弱命中转澄清
- 还是任务候选优先接住了请求

## 兼容与迁移

本轮采用直接替换，不加 feature flag。

原因：

- 现有多层裁决器并列正是问题本身
- 双链路并存只会让线上行为更难预测

兼容策略：

- 保留原有工具注册和执行接口
- 保留原有 `ActiveTask` 与会话存储
- 保留原有 `RetrievalResult`
- 尽量让改动集中在 `agent.go` 与 planner 层

## 测试策略

### 单测

- `planner_test.go`
  - 覆盖 `obvious_out / continue_task / clarify / tool / rag / mixed`
- `domain_gate_test.go`
  - 从二态改成 hint 判定
- `task_router_test.go`
  - 验证只做候选识别

### 集成测试

重点覆盖以下链路：

- `开启考勤订阅 -> 信工24级`
- `帮我补签 -> 张三 -> 今天第一节`
- `查这个群有没有订阅考勤推送`
- `你好`
- 业务内弱表达但知识可命中
- 业务内弱表达且知识弱命中，应该澄清而非拒答
- 明显站外消息，应该拒答

### 范围验证

- `go test ./internal/agent/... -count=1`
- `go test ./internal/app -run "TestCallLogAdapterPersistsDomainModeAndRetrievalDetails" -count=1`

## 风险

### 风险 1：弱表达消息过度放行

缓解：

- `obvious_out` 保持极保守
- 以澄清替代误拒答

### 风险 2：原有 intent 测试需要重写

缓解：

- 把 intent 相关断言降级为 planner evidence 或 helper 测试
- 不再把 intent 分类视为最终行为契约

### 风险 3：`agent.go` 重构范围较大

缓解：

- 先补 planner 决策单测
- 再按 TDD 替换主编排
- 以 `internal/agent/...` 范围回归作为每步 gate

## 结论

当前问题的根因不是“记忆没接好”，而是系统先裁决、后理解。

本轮重构将：

- 让会话状态优先于单轮关键词
- 让 RAG 成为 planner 的证据层
- 让拒答后置到明显站外
- 用统一 planner 替换多层并列抢答

这样才能从结构上解决：

- 上下文承接失败
- RAG 检索没机会参与
- 机械站外拒答过多
