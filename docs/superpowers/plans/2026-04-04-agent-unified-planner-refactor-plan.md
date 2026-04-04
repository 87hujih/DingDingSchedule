# Agent Unified Planner Refactor Implementation Plan

> **For agentic workers:** REQUIRED: Use superpowers:subagent-driven-development (if subagents available) or superpowers:executing-plans to implement this plan. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用统一 planner 替换当前 Agent 中并列抢答的 `conversation interpreter + domain gate + task router + query router + conditional RAG` 主决策链，让非明显站外消息默认先理解再回答，彻底消除“RAG 被饿死”和过早站外拒答。

**Architecture:** 保留现有工具注册、会话状态、槽位补全和知识检索实现，但把它们改成 planner 的证据层和执行层。新的 `Agent.chat` 只负责读取会话、收集证据、调用统一 planner、执行 `PlanDecision`、写日志；`domainGate` 降级为 `domain hint`，检索前置成 `retrieval prepass`，`taskRouter`/`queryRouter` 降级为 helper。

**Tech Stack:** Go, `internal/agent`, existing `internal/agent/tools` registry, existing RAG retrieval service, GORM-backed `agent_call_logs`

---

## File Map

- Modify: `tasks/todo.md`
  - 跟踪本轮 plan 执行状态、验证命令和复盘。
- Modify: `internal/agent/agent.go`
  - 把主流程改成 `load state -> gather evidence -> plan -> execute -> persist`。
- Create: `internal/agent/planner_types.go`
  - 定义 `DomainHint`、`PlanInput`、`PlanDecision`、`PlanKind`、`KnowledgeStrength` 等统一结构。
- Create: `internal/agent/planner.go`
  - 统一 planner 决策入口与纯规则决策逻辑。
- Create: `internal/agent/planner_test.go`
  - 锁定 `obvious_out / continue_task / clarify / tool / rag / mixed` 的决策边界。
- Modify: `internal/agent/domain_gate.go`
  - 从最终门禁改成 `obvious_out / likely_in / unknown` 的 hint。
- Modify: `internal/agent/domain_gate_test.go`
  - 调整为 hint 级别断言，不再测试最终拒答语义。
- Modify: `internal/agent/retrieval.go`
  - 增加 retrieval prepass helper 和 `none / weak / strong` 知识强度判断。
- Modify: `internal/agent/conversation_interpreter.go`
  - 保留会话事件解释，但让它只表达 `greeting / cancel / task_follow_up / new_request / unknown`。
- Modify: `internal/agent/conversation_interpreter_test.go`
  - 对齐新的会话优先级和补槽边界。
- Modify: `internal/agent/task_router.go`
  - 降级成任务候选识别器，不再直接决定主流程执行。
- Modify: `internal/agent/task_router_test.go`
  - 对齐任务候选识别和 `ActiveTask` 初始化语义。
- Modify: `internal/agent/slot_filler.go`
  - 继续做槽位补全，并明确返回 `matched slots`。
- Modify: `internal/agent/slot_filler_test.go`
  - 对齐 planner 需要的 matched slots 断言。
- Modify: `internal/agent/clarify.go`
  - 增加按 planner 原因输出澄清文案的 helper，例如 `missing_slots / weak_domain_match / weak_knowledge_match / ambiguous_request`。
- Modify: `internal/agent/clarify_test.go`
  - 锁定新的澄清文案和拒答边界。
- Modify: `internal/agent/query_router.go`
  - 去掉主裁决 API 的生产角色，仅保留 signal helper；若保留兼容 wrapper，必须明确标注不再被 `Agent.chat` 使用。
- Modify: `internal/agent/query_router_test.go`
  - 从“主流程路由测试”降级为 signal/helper 测试。
- Modify: `internal/agent/eval.go`
  - 如果离线评测仍依赖旧 `queryRouter` 结果，则改成使用 planner 或 planner-compatible adapter。
- Modify: `internal/agent/eval_test.go`
  - 对齐 planner 决策口径。
- Modify: `internal/agent/testdata/eval_cases.json`
  - 必要时补充 `expected_plan_kind` 或等价字段，避免继续拿旧 `answerModeReject` 当主路径契约。
- Modify: `internal/agent/agent_rag_test.go`
  - 覆盖弱表达消息、RAG 前置、明显站外、跟进补槽、多轮切换等端到端路径。
- Modify: `internal/agent/tools/types.go`
  - 调用日志增加 planner 证据字段，例如 `domain_hint / plan_kind / knowledge_strength / planner_reason`。
- Modify: `internal/model/agent_call_log.go`
  - 持久化新增 planner 日志字段。
- Modify: `internal/app/agent_wiring.go`
  - 写入新增 planner 日志字段。
- Modify: `internal/app/agent_wiring_test.go`
  - 锁定 call log adapter 的 planner 字段持久化。

## Phase Ordering

1. 先在隔离 worktree 里建立基线，避免根工作区未跟踪文件和 pre-commit hook 干扰实现。
2. 先写 planner 的纯决策单测和 `DomainHint` 单测，锁定新的决策顺序。
3. 再把 retrieval prepass、task candidate、signal helper 接到 planner 输入，避免 `agent.go` 里继续散落决策。
4. 然后重写 `Agent.chat` 主编排，去掉硬前置拒答。
5. 最后刷新日志、离线评测和端到端回归，确认行为和可观测性都落地。

### Task 0: 建立隔离 worktree 和范围基线

**Files:**
- Modify: `tasks/todo.md`

- [ ] **Step 1: 创建独立 worktree**

Run:

```bash
git worktree add .worktrees/agent-unified-planner-refactor -b codex/agent-unified-planner-refactor
```

要求：

- 在新 worktree 内执行后续所有实现
- 不直接在根工作区里做代码改动

- [ ] **Step 2: 进入 worktree 并拉取 Go 依赖**

Run:

```bash
Set-Location .worktrees/agent-unified-planner-refactor
go mod download
```

- [ ] **Step 3: 建立范围基线**

Run:

```bash
go test ./internal/agent/... -count=1
go test ./internal/app -run "TestCallLogAdapterPersistsDomainModeAndRetrievalDetails" -count=1
```

可选附加基线：

```bash
go test ./... -count=1
```

Expected:

- `internal/agent/...` 与目标 `internal/app` 适配测试通过
- 如果 `go test ./...` 仍因既有 `internal/ci` 失败而不绿，记录为基线事实，不把它算进本轮回归

- [ ] **Step 4: 更新任务跟踪**

在 `tasks/todo.md` 顶部记录：

- worktree 路径
- 范围基线命令
- 如果存在全仓预存失败，明确写出失败测试名

- [ ] **Step 5: 提交准备性变更**

```bash
git add -f tasks/todo.md
git commit -m "agent统一planner重构准备"
```

### Task 1: 先用 TDD 锁定统一 planner 和 domain hint

**Files:**
- Create: `internal/agent/planner_types.go`
- Create: `internal/agent/planner.go`
- Create: `internal/agent/planner_test.go`
- Modify: `internal/agent/domain_gate.go`
- Modify: `internal/agent/domain_gate_test.go`

- [ ] **Step 1: 先写 planner 的失败测试**

在 `internal/agent/planner_test.go` 新增至少以下测试：

```go
func TestPlannerReturnsObviousOutForClearlyIrrelevantRequest(t *testing.T) {}
func TestPlannerPrefersContinueTaskOverDomainHint(t *testing.T) {}
func TestPlannerReturnsClarifyForWeakInDomainRequest(t *testing.T) {}
func TestPlannerReturnsRAGForStrongKnowledgeWithoutLiveSignal(t *testing.T) {}
func TestPlannerReturnsMixedForStrongKnowledgeWithLiveSignal(t *testing.T) {}
func TestPlannerReturnsToolForActionWithoutStrongKnowledge(t *testing.T) {}
```

这些测试要直接断言 `PlanDecision.Kind`，不要再绕回旧 `answerMode`。

- [ ] **Step 2: 先写 domain hint 的失败测试**

在 `internal/agent/domain_gate_test.go` 中把旧二态断言改成三态断言，例如：

```go
func TestDomainHintReturnsObviousOutForWeatherQuestion(t *testing.T) {}
func TestDomainHintReturnsLikelyInForAttendanceRuleQuestion(t *testing.T) {}
func TestDomainHintReturnsUnknownForShortAmbiguousBusinessLikeMessage(t *testing.T) {}
```

- [ ] **Step 3: 跑测试确认先红**

Run:

```bash
go test ./internal/agent -run "TestPlanner|TestDomainHint" -count=1
```

Expected:

- FAIL
- 原因是 planner 结构和新的 domain hint 语义尚未实现

- [ ] **Step 4: 实现最小 planner 类型和 domain hint**

在 `planner_types.go` 中定义最小结构：

```go
type DomainHint string
type PlanKind string
type KnowledgeStrength string

type PlanInput struct { /* see spec */ }
type PlanDecision struct { /* see spec */ }
```

在 `domain_gate.go` 中实现：

```go
func (g *domainGate) Hint(question string) DomainHint
```

要求：

- `domainGate` 不再返回最终裁决
- 只允许把极明确的站外问题判成 `obvious_out`
- 弱业务表达默认进 `unknown`

- [ ] **Step 5: 实现最小 planner 决策顺序**

在 `planner.go` 中实现纯函数：

```go
func plan(input PlanInput) PlanDecision
```

先只覆盖这些优先级：

1. `greeting`
2. `cancel + active task`
3. `task_follow_up + active task`
4. `obvious_out`
5. `task candidate`
6. `strong knowledge + live`
7. `strong knowledge + no live`
8. `action/live`
9. fallback `clarify`

- [ ] **Step 6: 重跑测试确认转绿**

Run:

```bash
go test ./internal/agent -run "TestPlanner|TestDomainHint" -count=1
```

Expected:

- PASS

- [ ] **Step 7: 提交这一阶段**

```bash
git add internal/agent/planner_types.go internal/agent/planner.go internal/agent/planner_test.go internal/agent/domain_gate.go internal/agent/domain_gate_test.go
git commit -m "agent引入统一planner决策"
```

### Task 2: 把 retrieval prepass 和知识强度改成 planner 证据

**Files:**
- Modify: `internal/agent/retrieval.go`
- Modify: `internal/agent/planner.go`
- Modify: `internal/agent/planner_test.go`

- [ ] **Step 1: 为知识强度判断补失败测试**

在 `planner_test.go` 中补至少以下场景：

```go
func TestPlannerTreatsNoHitsAsClarifyInsteadOfReject(t *testing.T) {}
func TestPlannerTreatsWeakKnowledgeAsClarify(t *testing.T) {}
func TestPlannerTreatsStrongKnowledgeAsRAGOrMixed(t *testing.T) {}
```

- [ ] **Step 2: 跑测试确认先红**

Run:

```bash
go test ./internal/agent -run "TestPlannerTreats" -count=1
```

Expected:

- FAIL

- [ ] **Step 3: 在 `retrieval.go` 中增加知识强度 helper**

增加最小 helper：

```go
func classifyKnowledgeStrength(result RetrievalResult) KnowledgeStrength
```

建议首版规则：

- `none`: 0 hits
- `weak`: hits > 0 但最高分 < 强命中阈值
- `strong`: hits > 0 且最高分 >= 强命中阈值

要求：

- 不修改底层检索调用
- 只把分类逻辑从 `queryRouter` 里抽出来

- [ ] **Step 4: 在 planner 中使用 `KnowledgeStrength`**

要求：

- 不再依赖旧 `answerModeReject`
- `weak` 和 `none` 默认进入 `clarify` 或 `tool`，绝不直接站外拒答

- [ ] **Step 5: 重跑测试确认转绿**

Run:

```bash
go test ./internal/agent -run "TestPlannerTreats|TestPlannerReturns" -count=1
```

Expected:

- PASS

- [ ] **Step 6: 提交这一阶段**

```bash
git add internal/agent/retrieval.go internal/agent/planner.go internal/agent/planner_test.go
git commit -m "agent将检索结果接入planner"
```

### Task 3: 降级 task/query router 为 planner helper

**Files:**
- Modify: `internal/agent/task_router.go`
- Modify: `internal/agent/task_router_test.go`
- Modify: `internal/agent/slot_filler.go`
- Modify: `internal/agent/slot_filler_test.go`
- Modify: `internal/agent/query_router.go`
- Modify: `internal/agent/query_router_test.go`
- Modify: `internal/agent/conversation_interpreter.go`
- Modify: `internal/agent/conversation_interpreter_test.go`

- [ ] **Step 1: 先补 helper 语义的失败测试**

把现有测试改成 helper 契约，不再测试它们直接裁决主流程。例如：

```go
func TestBuildTaskCandidateReturnsSubscriptionTask(t *testing.T) {}
func TestFillTaskSlotsReturnsMatchedSlotNames(t *testing.T) {}
func TestConversationInterpreterReturnsUnknownForAmbiguousFollowUp(t *testing.T) {}
func TestSignalHelpersDoNotOwnFinalRoutingDecision(t *testing.T) {}
```

- [ ] **Step 2: 跑测试确认先红**

Run:

```bash
go test ./internal/agent -run "TestBuildTask|TestFillTaskSlots|TestInterpretConversation|TestSignal" -count=1
```

Expected:

- FAIL

- [ ] **Step 3: 将 task router 改成任务候选识别器**

要求：

- 保留 `ActiveTask` 建模和槽位初始化
- 不再由 task router 决定是否直接执行
- 命名可以继续用 `buildTaskFromRequest`，但注释要改成 candidate 语义

- [ ] **Step 4: 让 slot filler 返回 matched slot 信息**

最小结构参考：

```go
type slotFillResult struct {
    Filled       map[string]string
    MatchedSlots []string
    Ready        bool
}
```

要求：

- `MatchedSlots` 顺序稳定，便于日志断言

- [ ] **Step 5: 把 `query_router.go` 降级成 signal helper 容器**

要求：

- `Agent.chat` 后续不再依赖它的 `DecideIntent` / `Decide`
- 如果为了编译兼容保留旧接口，必须在注释里明确“仅供旧评测或过渡代码使用”
- 新的主行为测试不能再以它为准

- [ ] **Step 6: 重跑 helper 范围测试**

Run:

```bash
go test ./internal/agent -run "TestBuildTask|TestFillTaskSlots|TestInterpretConversation|TestSignal" -count=1
```

Expected:

- PASS

- [ ] **Step 7: 提交这一阶段**

```bash
git add internal/agent/task_router.go internal/agent/task_router_test.go internal/agent/slot_filler.go internal/agent/slot_filler_test.go internal/agent/query_router.go internal/agent/query_router_test.go internal/agent/conversation_interpreter.go internal/agent/conversation_interpreter_test.go
git commit -m "agent收口planner辅助信号"
```

### Task 4: 重写 `Agent.chat` 主编排

**Files:**
- Modify: `internal/agent/agent.go`
- Modify: `internal/agent/clarify.go`
- Modify: `internal/agent/clarify_test.go`
- Modify: `internal/agent/planner.go`
- Modify: `internal/agent/planner_types.go`

- [ ] **Step 1: 先补主编排的失败测试**

在 `internal/agent/agent_rag_test.go` 之外，优先用较小测试覆盖主编排关键行为：

```go
func TestAgentDoesNotRejectUnknownBusinessLikeMessageBeforeRetrieval(t *testing.T) {}
func TestAgentUsesRetrievalPrepassForNonObviousOutRequest(t *testing.T) {}
func TestAgentClarifiesWeakKnowledgeMatchInsteadOfRejecting(t *testing.T) {}
```

如果这些场景只能在 `agent_rag_test.go` 里覆盖，也要先写并确认失败。

- [ ] **Step 2: 跑测试确认先红**

Run:

```bash
go test ./internal/agent -run "TestAgent(DoesNotRejectUnknownBusinessLikeMessageBeforeRetrieval|UsesRetrievalPrepassForNonObviousOutRequest|ClarifiesWeakKnowledgeMatchInsteadOfRejecting)" -count=1
```

Expected:

- FAIL

- [ ] **Step 3: 在 `agent.go` 中改成统一证据收集**

要求：

- 先读取 `history` 和 `active_task`
- 先跑 `interpretConversation`
- 对 `not obvious_out` 的新请求跑 retrieval prepass
- 收集 `task candidate` 和 `signal helpers`
- 把这些统一交给 `plan(input)`

禁止：

- 再出现“domain gate 先拥有最终拒答权”
- 再出现“必须先判成 rule/mixed 才能检索”

- [ ] **Step 4: 新建执行分发 helper**

把 `PlanDecision` 映射到执行路径：

- `PlanObviousOut` -> 站外拒答
- `PlanContinueTask` -> `respondForTaskState`
- `PlanClarify` -> 新澄清回复
- `PlanTool` -> 走现有工具/clarify 路径
- `PlanRAG` -> 走知识回答
- `PlanMixed` -> 走 mixed + tools

要求：

- `agent.go` 里的执行分支按 `PlanKind` 收口
- 不再用旧 `answerModeReject` 作为领域内兜底

- [ ] **Step 5: 在 `clarify.go` 中对齐 planner 原因**

增加按原因输出文案的 helper，例如：

```go
func buildPlannerClarifyReply(reason string, task *ActiveTask) string
```

至少覆盖：

- `missing_slots`
- `weak_domain_match`
- `weak_knowledge_match`
- `ambiguous_request`

- [ ] **Step 6: 重跑定向测试确认转绿**

Run:

```bash
go test ./internal/agent -run "TestAgent(DoesNotRejectUnknownBusinessLikeMessageBeforeRetrieval|UsesRetrievalPrepassForNonObviousOutRequest|ClarifiesWeakKnowledgeMatchInsteadOfRejecting)" -count=1
go test ./internal/agent -run "TestBuildTaskClarifyReplyFor|TestBuildUnknownFollowUpReply" -count=1
```

Expected:

- PASS

- [ ] **Step 7: 提交这一阶段**

```bash
git add internal/agent/agent.go internal/agent/clarify.go internal/agent/clarify_test.go internal/agent/planner.go internal/agent/planner_types.go
git commit -m "agent接入统一planner主流程"
```

### Task 5: 扩展日志与离线评测口径

**Files:**
- Modify: `internal/agent/tools/types.go`
- Modify: `internal/model/agent_call_log.go`
- Modify: `internal/app/agent_wiring.go`
- Modify: `internal/app/agent_wiring_test.go`
- Modify: `internal/agent/eval.go`
- Modify: `internal/agent/eval_test.go`
- Modify: `internal/agent/testdata/eval_cases.json`

- [ ] **Step 1: 先补日志适配层失败测试**

在 `internal/app/agent_wiring_test.go` 里补 planner 字段断言，例如：

```go
if row.ConversationEvent != "task_follow_up" { ... }
if row.DomainHint != "unknown" { ... }
if row.PlanKind != "clarify" { ... }
if row.KnowledgeStrength != "weak" { ... }
```

- [ ] **Step 2: 先补离线评测失败测试**

如果 `eval.go` 仍拿旧 `queryRouter` 当契约，先加测试锁定新契约：

```go
func TestEvaluateCasesAggregatesPlannerDecisionMatches(t *testing.T) {}
```

- [ ] **Step 3: 跑测试确认先红**

Run:

```bash
go test ./internal/app -run "TestCallLogAdapterPersistsDomainModeAndRetrievalDetails" -count=1
go test ./internal/agent -run "TestEvaluateCases" -count=1
```

Expected:

- FAIL

- [ ] **Step 4: 增加 planner 日志字段**

在 `CallLog` / `AgentCallLog` 中新增并打通：

- `DomainHint`
- `PlanKind`
- `KnowledgeStrength`
- `PlannerReason`

要求：

- 不删除已有会话事件和任务状态字段
- adapter 测试覆盖到数据库持久化结果

- [ ] **Step 5: 对齐 `eval.go`**

要求：

- 新的主口径应该围绕 planner 决策，而不是旧 `answerModeReject`
- 若保留旧字段，必须作为兼容输出，不再作为主评判标准

- [ ] **Step 6: 重跑测试确认转绿**

Run:

```bash
go test ./internal/app -run "TestCallLogAdapterPersistsDomainModeAndRetrievalDetails" -count=1
go test ./internal/agent -run "TestEvaluateCases" -count=1
```

Expected:

- PASS

- [ ] **Step 7: 提交这一阶段**

```bash
git add internal/agent/tools/types.go internal/model/agent_call_log.go internal/app/agent_wiring.go internal/app/agent_wiring_test.go internal/agent/eval.go internal/agent/eval_test.go internal/agent/testdata/eval_cases.json
git commit -m "agent补充planner日志与评测"
```

### Task 6: 端到端回归与范围验证

**Files:**
- Modify: `internal/agent/agent_rag_test.go`
- Modify: `tasks/todo.md`

- [ ] **Step 1: 先补端到端失败回归**

在 `internal/agent/agent_rag_test.go` 中至少覆盖：

```go
func TestAgentChatKeepsWeakBusinessLikeMessageInClarifyInsteadOfOutOfDomain(t *testing.T) {}
func TestAgentChatRunsRetrievalPrepassBeforeRejecting(t *testing.T) {}
func TestAgentChatUsesMixedForStrongKnowledgePlusRealtimeSignal(t *testing.T) {}
func TestAgentChatRejectsOnlyClearlyOutOfDomainMessage(t *testing.T) {}
func TestAgentChatResumesTaskFollowUpBeforeAnyDomainHintCheck(t *testing.T) {}
```

- [ ] **Step 2: 跑这些测试确认先红**

Run:

```bash
go test ./internal/agent -run "TestAgentChat(KeepsWeakBusinessLikeMessageInClarifyInsteadOfOutOfDomain|RunsRetrievalPrepassBeforeRejecting|UsesMixedForStrongKnowledgePlusRealtimeSignal|RejectsOnlyClearlyOutOfDomainMessage|ResumesTaskFollowUpBeforeAnyDomainHintCheck)" -count=1
```

Expected:

- FAIL

- [ ] **Step 3: 刷新现有端到端断言**

要求：

- 删除或调整任何仍在锁定旧 `queryRouter` 主路径的断言
- 保留对真实用户体验有意义的断言：
  - 是否误拒答
  - 是否继续任务
  - 是否触发检索
  - 是否走 tool / rag / mixed

- [ ] **Step 4: 跑范围回归**

Run:

```bash
go test ./internal/agent/... -count=1
go test ./internal/app -run "TestCallLogAdapterPersistsDomainModeAndRetrievalDetails" -count=1
git diff --check -- internal/agent internal/app internal/model tasks/todo.md
```

如果本轮 worktree 中 `go test ./... -count=1` 仍只失败在既有 `internal/ci`，则补充记录对比结果：

```bash
go test ./... -count=1
```

- [ ] **Step 5: 更新复盘**

在 `tasks/todo.md` 顶部记录：

- worktree 路径
- 各阶段验证命令
- 是否存在全仓既有失败
- 这次 planner 重构具体解决了哪些错误拒答路径

- [ ] **Step 6: 提交这一阶段**

```bash
git add internal/agent/agent_rag_test.go tasks/todo.md
git commit -m "agent完成统一planner回归验证"
```

## Execution Notes

- 这份计划默认在隔离 worktree 中执行，避免根工作区已有未跟踪文件影响 `go vet` 或提交 hook。
- 当前根工作区存在与本轮无关的未跟踪 Go 文件，因此文档提交和后续实现都不应依赖根工作区的 hook 结果来判断本轮改动质量。
- 如果实现中发现 `query_router.go` 和 `eval.go` 的旧契约拆除成本过高，可以保留兼容 wrapper，但必须确保 `Agent.chat` 已完全不再依赖它们作为主裁决器。
- 如果任何阶段出现“又想在 `domainGate` 上加特判”的冲动，说明实现已经偏离统一 planner 目标，应停下来回到 spec 检查。
