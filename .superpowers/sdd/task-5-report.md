# Task 5 Report

## 实现

- LLM `context.DeadlineExceeded` 归类为 `timeout`，其他错误归类为 `error`。
- LLM 失败后保留 deterministic candidates；仅当候选等价、目录 operation/act 合法且风险允许时返回 `Source=fallback`。
- catalog 精确低风险写仍受 catalog 风险约束；legacy/inferred write 一律 fail-closed。
- 不安全或歧义 fallback 返回不可执行 unknown，并记录低基数 `llm_timeout` / `llm_error`，不保存原始错误文本。
- compiler source/status/fallback reason 传入 protocol outcome 和 call metrics。
- fallback 成功继续经过正常 pre-policy、catalog dispatch、resolver/write guard/executor；编译 timeout 不覆盖最终成功语义。

## TDD 证据

### RED

命令：

```powershell
go test ./internal/agent -run "OperationCompilerFallsBackToExactReadOnLLMTimeout|OperationCompilerInferredWriteFailsClosedOnLLMTimeout|ProtocolLivePipelineCompilerTimeoutFallbackPreservesSuccessfulOutcomeAndMetrics" -count=1
```

结果：失败。错误仍由 operation compiler 向上返回，并且 `protocolLiveOutcome` / `protocolMetrics` 尚无 `CompilerSource`、`CompilerFallbackReason` 字段。

首次最小实现后，read fallback 仍因同一 operation 的 alias + deterministic read 重复证据被当作歧义而失败；随后仅折叠 act/domain/operation 完全相同的证据，不同候选继续 fail-closed。

### GREEN

```powershell
go test ./internal/agent -run "OperationCompilerFallsBackToExactReadOnLLMTimeout|OperationCompilerInferredWriteFailsClosedOnLLMTimeout|ProtocolLivePipelineCompilerTimeoutFallbackPreservesSuccessfulOutcomeAndMetrics" -count=1
```

结果：`ok schedule_server/internal/agent`

验收命令：

```powershell
go test ./internal/agent -run "OperationCompiler|ProtocolLivePipeline|Compiler" -count=1
go test ./internal/agent -count=1
```

结果：两者均通过。

## 自审

- 未调用 legacy semantic router/planner/ReAct/ToolRegistry。
- inferred/fuzzy write timeout 用例返回 unknown，executor 写调用为 0。
- fallback operation 仍必须存在于 catalog 且 act 被 manifest 允许。
- 多个不同 act/domain/operation 的候选均拒绝 fallback；只有同一候选的重复 deterministic 证据可折叠。
- 原始 LLM error 未进入 outcome、metrics、draft 或日志字段。
- 兼容保留 `compileProtocolWithCompiler` 原接口，新增结构化结果接口供 live pipeline 使用。
- `agent.go` 仅增加 outcome 到 call metrics 的两处字段赋值，属于元数据传播所必需的最小改动。

## Commit

提交主题：`编译器错误时增加安全降级`
