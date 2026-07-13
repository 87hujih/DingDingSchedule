# Agent Subscription Selection Fix Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让群考勤订阅在明确部门名、`3. 部门名` 候选选择和中途切换“全部人员”时都能使用可信参数正常执行。

**Architecture:** 保持 `protocol_live` 现有 catalog/policy/workflow/executor 链路，仅在部门专用规范化、候选 resolver、订阅 workflow 转移和结构化 clarify 处做确定性修复。所有写操作仍只消费 tenant-scoped trusted params，不让 LLM 直接提供可执行 ID。

**Tech Stack:** Go 1.x, existing `internal/agent` protocol-live pipeline, table-driven Go tests, GORM-backed production runtime unchanged.

## Global Constraints

- 生产基线是 `origin/master@6ec7664`，不在落后的本地 `main` 上实施。
- 不修改 LLM prompt、operation catalog、权限策略、write guard、幂等或 workflow fencing。
- 不允许序号与显式标签不一致时选中候选。
- `scope=all` 必须清理任何旧 `dept_ids`，避免参数混用。
- Agent 回复保持纯文本。

---

### Task 1: 部门专用规范化和候选显示格式解析

**Files:**
- Modify: `internal/agent/entity_resolver.go`
- Modify: `internal/agent/entity_resolver_test.go`
- Modify: `internal/agent/candidate_resolver.go`
- Modify: `internal/agent/candidate_resolver_test.go`

**Interfaces:**
- Consumes: `entityNameVariants(string) []string`, `parseCandidateOrdinal(string) (int, bool)`, `validateCandidateTenant(Candidate, uint)`.
- Produces: `departmentNameVariants(string) []string` 和对 `resolveCandidateSelection` 的扩展，公开类型签名不变。

- [ ] **Step 1: 写部门后缀红灯测试**

在 `entity_resolver_test.go` 增加表驱动断言：

```go
func TestResolveDepartmentSlotAcceptsDepartmentSuffix(t *testing.T) {
	result := resolveDepartmentSlotWithContext(
		EntityResolveContext{TenantID: 2}.RawSlot("dept_ids", "26暑期智能体开发训练营部门"),
		[]tools.DeptItem{{DeptID: 1083420327, Name: "26暑期智能体开发训练营", TenantID: 2}},
	)
	if result.Status != ResolveResolved {
		t.Fatalf("Status = %q, want resolved", result.Status)
	}
}
```

- [ ] **Step 2: 运行红灯**

Run: `go test ./internal/agent -run TestResolveDepartmentSlotAcceptsDepartmentSuffix -count=1 -v`

Expected: FAIL，旧 resolver 返回 `not_found`。

- [ ] **Step 3: 写候选显示格式与 mismatch 红灯测试**

在 `candidate_resolver_test.go` 增加：

```go
func TestResolveCandidateSelectionMatchesRenderedOrdinalAndLabel(t *testing.T) {
	candidates := []Candidate{
		{ID: "101", Label: "26鹰飞前端", TenantID: 2},
		{ID: "1083420327", Label: "26暑期智能体开发训练营", TenantID: 2},
	}
	got := resolveCandidateSelection(CandidateSelectionInput{
		Message: "2. 26暑期智能体开发训练营", TenantID: 2, Candidates: candidates,
	})
	if !got.Handled || !got.OK || got.Candidate.ID != "1083420327" {
		t.Fatalf("selection = %+v", got)
	}
	mismatch := resolveCandidateSelection(CandidateSelectionInput{
		Message: "1. 26暑期智能体开发训练营", TenantID: 2, Candidates: candidates,
	})
	if !mismatch.Handled || mismatch.OK || mismatch.Reason != "candidate_ordinal_label_mismatch" {
		t.Fatalf("mismatch = %+v", mismatch)
	}
}
```

- [ ] **Step 4: 运行候选红灯**

Run: `go test ./internal/agent -run TestResolveCandidateSelectionMatchesRenderedOrdinalAndLabel -count=1 -v`

Expected: FAIL，旧 parser 无法识别 `2. 标签`。

- [ ] **Step 5: 实现最小规范化和候选校验**

`departmentNameVariants` 先返回现有变体，再仅对末尾 `部门` 增加剔除变体；部门精确/归一化/候选匹配改用该函数。候选 resolver 解析行首阿拉伯数字或中文序号及 `.`, `、`, `:`, `：`, `)` 分隔符；存在剩余标签时对比选中候选的 `entityNameVariants`。

- [ ] **Step 6: 运行 Task 1 绿灯**

Run: `go test ./internal/agent -run "TestResolve(DepartmentSlotAcceptsDepartmentSuffix|CandidateSelection)" -count=1 -v`

Expected: PASS，包括旧纯序号、精确标签和跨租户测试。

### Task 2: workflow 切换和真实缺失字段提示

**Files:**
- Modify: `internal/agent/workflow_engine.go`
- Modify: `internal/agent/workflow_engine_test.go`
- Modify: `internal/agent/protocol_live_subscription_workflow.go`
- Modify: `internal/agent/response_renderer.go`
- Modify: `internal/agent/response_renderer_test.go`

**Interfaces:**
- Consumes: `continueSubscriptionWorkflow`, `workflowMissingFields`, `ResponseModel`.
- Produces: `collect_departments -> all -> ready_to_execute` 转移和 `dept_names` 专用提示。

- [ ] **Step 1: 写 workflow 切换红灯**

```go
func TestContinueSubscriptionWorkflowSwitchesDepartmentSelectionToAllScope(t *testing.T) {
	wf := WorkflowSnapshot{Type: WorkflowSubscriptionStart, State: WorkflowCollectDepartments,
		MissingFields: []string{"dept_names"}, Trusted: trustedEntities{Scope: "department", DeptIDs: []int64{101}, TrustedParams: map[string]TrustedParam{"dept_ids": {Field: "dept_ids", Value: []int64{101}}}}}
	result := continueSubscriptionWorkflow(wf, ProtocolDraft{Act: ActWorkflowContinue, Operation: "subscription.start"}, trustedEntities{Scope: "all"})
	if result.Decision != WorkflowReadyToExecute || result.Workflow.Trusted.Scope != "all" || len(result.Workflow.Trusted.DeptIDs) != 0 {
		t.Fatalf("result = %+v", result)
	}
	if _, exists := result.Workflow.Trusted.TrustedParams["dept_ids"]; exists {
		t.Fatal("dept_ids must be cleared for all scope")
	}
}
```

- [ ] **Step 2: 写提示红灯**

```go
func TestRenderProtocolResponseClarifiesMissingSubscriptionDepartments(t *testing.T) {
	reply := renderProtocolResponse(ResponseModel{Kind: ResponseClarify, Operation: "subscription.start", MissingFields: []string{"dept_names"}})
	if !strings.Contains(reply, "部门选项") || strings.Contains(reply, "选择订阅范围") {
		t.Fatalf("reply = %q", reply)
	}
}
```

- [ ] **Step 3: 运行红灯**

Run: `go test ./internal/agent -run "Test(ContinueSubscriptionWorkflowSwitchesDepartmentSelectionToAllScope|RenderProtocolResponseClarifiesMissingSubscriptionDepartments)" -count=1 -v`

Expected: FAIL，旧 workflow 拒绝 all scope，旧 renderer 无部门缺失提示。

- [ ] **Step 4: 实现最小状态转移和结构化 clarify**

在 `WorkflowCollectDepartments` 对 `trusted.Scope == "all"` 设置 all scope、清理 department fields/param 并返回 ready。在 `resolveSubscriptionTrustedEntities` 的部门收集状态先识别 all scope。解析失败时 ResponseModel 填入 `Operation: startOperation` 和 `MissingFields: workflowMissingFields(activeWorkflow)`。`renderMissingFieldsClarify` 增加 `dept_names` 文案。

- [ ] **Step 5: 运行 Task 2 绿灯**

Run: `go test ./internal/agent -run "Test(ContinueSubscriptionWorkflow|RenderProtocolResponseClarifiesMissingSubscription)" -count=1 -v`

Expected: PASS。

### Task 3: 生产对话 pipeline 回放

**Files:**
- Modify: `internal/agent/protocol_live_pipeline_test.go`

**Interfaces:**
- Consumes: existing `executorFakeGroupSubPort`, `protocolLivePipeline`, workflow candidates and trusted params.
- Produces: 端到端证据，确认 resolver 结果实际到达 subscription executor。

- [ ] **Step 1: 写候选回放红灯**

复用现有订阅 pipeline fixture，构造 `collect_departments` workflow 及 4 个生产候选，输入 `3. 26暑期智能体开发训练营`，断言：

```go
if groupSub.subscribeCalls != 1 || !reflect.DeepEqual(groupSub.lastDeptIDs, []int64{1083420327}) {
	t.Fatalf("calls=%d deptIDs=%v", groupSub.subscribeCalls, groupSub.lastDeptIDs)
}
if !outcome.ClearWorkflow || outcome.Response.Kind != ResponseResult {
	t.Fatalf("outcome = %+v", outcome)
}
```

- [ ] **Step 2: 写 all-scope 回放红灯**

使用同样 `collect_departments` workflow 输入 `全部人员`，断言 `subscribeCalls == 1`、`lastDeptIDs` 为空、ResponseResult 且 workflow 清除。

- [ ] **Step 3: 运行 pipeline 红灯**

Run: `go test ./internal/agent -run "TestProtocolLivePipelineSubscription(ExecutesRenderedDepartmentSelection|SwitchesDepartmentSelectionToAllScope)" -count=1 -v`

Expected: 两个测试均 FAIL，且失败点分别是 executor 未调用和 workflow 未进入 ready。

- [ ] **Step 4: 运行实现后 pipeline 绿灯**

Run: 同 Step 3。

Expected: PASS。

- [ ] **Step 5: 运行相关回归**

Run: `go test ./internal/agent -run "Test(ResolveCandidateSelection|ResolveDepartment|ContinueSubscriptionWorkflow|ProtocolLivePipeline.*Subscription|RenderProtocolResponseClarifiesMissingSubscription)" -count=1 -v`

Expected: PASS，无 warning/panic。

### Task 4: 全量验证、审查和推送

**Files:**
- Modify: `tasks/todo.md` (local ignored task record)

**Interfaces:**
- Consumes: Tasks 1-3 的全部变更。
- Produces: 可审查提交、远端推送证据和部署影响说明。

- [ ] **Step 1: 格式化与静态检查**

Run: `gofmt -w internal/agent/entity_resolver.go internal/agent/entity_resolver_test.go internal/agent/candidate_resolver.go internal/agent/candidate_resolver_test.go internal/agent/workflow_engine.go internal/agent/workflow_engine_test.go internal/agent/protocol_live_subscription_workflow.go internal/agent/protocol_live_pipeline_test.go internal/agent/response_renderer.go internal/agent/response_renderer_test.go`

Run: `gofmt -l internal/agent`

Expected: 第二条无输出。

- [ ] **Step 2: Agent 包、race 和全仓测试**

Run: `go test ./internal/agent/... -count=1`

Run: `go test -race ./internal/agent/... -count=1`

Run: `go test ./... -count=1`

Expected: 全部 exit 0。

- [ ] **Step 3: build、vet、lint 与 diff 检查**

Run: `go build -o bin/schedule_server ./cmd/main.go`

Run: `go vet ./...`

Run: `golangci-lint run --timeout=5m --new-from-rev=origin/master`

Run: `git diff --check origin/master...HEAD`

Expected: 全部 exit 0；lint 不得忽略新问题。

- [ ] **Step 4: 完成两阶段审查**

要求 reviewer 分别检查设计契约和代码质量；修正所有 critical/important 问题并重跑受影响验证。

- [ ] **Step 5: 提交代码**

```powershell
git add internal/agent/entity_resolver.go internal/agent/entity_resolver_test.go internal/agent/candidate_resolver.go internal/agent/candidate_resolver_test.go internal/agent/workflow_engine.go internal/agent/workflow_engine_test.go internal/agent/protocol_live_subscription_workflow.go internal/agent/protocol_live_pipeline_test.go internal/agent/response_renderer.go internal/agent/response_renderer_test.go
git commit -m "agent修复订阅部门选择"
```

- [ ] **Step 6: 推送和核对**

Run: `git push origin HEAD:master`

Expected: 普通 fast-forward push 成功，远端 `origin/master` 指向本地 HEAD，并触发生产部署 workflow。

- [ ] **Step 7: 推送后验证**

核对 GitHub Actions 部署结果、生产容器镜像和 `/health`。如有可用的真实群聊新消息，再核对 `agent_call_logs.executor_status=success`、workflow 清除和群订阅记录；不伪造用户信息或主动更改生产订阅数据。
