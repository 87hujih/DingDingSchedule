# Agent RAG Eval

这份说明用于运行 retrieval-first 版本的 `RAG + Eval` 验证，评估领域门禁、回答模式、知识检索以及可选的端到端 Agent 回复效果。

## 评测范围

- `domain`：问题是否被正确判为 `in_domain / out_of_domain`
- `mode`：问题是否被正确判为 `knowledge-only / tool-first / mixed / reject`
- `route`：兼容旧口径，按 `mode` 映射出的 `tool / rag / mixed`
- `retrieval`：规则类问题是否命中预期知识片段
- `tools`：端到端模式下是否调用了预期工具
- `keywords`：端到端模式下回复是否包含预期关键词

首版样本位于 `internal/agent/testdata/eval_cases.json`，覆盖：

- 纯工具查询
- 纯规则问答
- 混合问答
- 非职责范围拒答

每条样本当前显式包含：

- `expected_domain`
- `expected_mode`
- `expected_route`
- `expected_sources`
- `expected_tools`
- `expected_keywords`

## 准备知识库

推荐先同步 `docs/agent-knowledge` 下的首批知识文档：

```bash
go run ./scripts/sync_agent_knowledge -tenant-id 1
```

也可以直接让评测脚本在执行前自动同步：

```bash
go run ./scripts/agent_eval -tenant-id 1 -sync-knowledge-root ./docs/agent-knowledge
```

## 只跑路由与检索

不依赖真实 Agent 用户身份，只验证 `domain + mode + route + retrieval`：

```bash
go run ./scripts/agent_eval -tenant-id 1 -sync-knowledge-root ./docs/agent-knowledge
```

输出示例关注：

- `领域准确率`
- `模式准确率`
- `路由准确率`
- `知识命中率`
- `平均耗时`
- `失败样本`

## 运行端到端评测

如果要统计 `工具命中率` 和 `关键词命中率`，需要提供一组真实可用的钉钉身份：

```bash
go run ./scripts/agent_eval \
  -tenant-id 1 \
  -sync-knowledge-root ./docs/agent-knowledge \
  -with-agent \
  -corp-id dingxxxx \
  -sender-id userxxxx \
  -sender-name Alice
```

端到端模式会：

- 调用真实 `Agent.Chat`
- 为每条评测样本创建独立的短生命周期 `Agent`，避免限流器和历史会话串样本
- 捕获本次对话工具调用列表
- 对照样本中的 `expected_tools` / `expected_keywords` 做匹配

## 指标解释

- `路由准确率`：`expected_route` 命中率
- `领域准确率`：`expected_domain` 命中率
- `模式准确率`：`expected_mode` 命中率
- `知识命中率`：规则类问题是否至少命中一个预期 `source_ref`
- `工具命中率`：实际工具调用是否覆盖 `expected_tools`
- `关键词命中率`：回复是否包含全部 `expected_keywords`
- `平均耗时`：单样本总耗时平均值

## 最近一次验证

`2026-03-30` 在 `agent-rag-rework` worktree 中，使用主仓库配置目录执行了一次 retrieval-first 离线评测：

```bash
CONFIG_PATH=G:\gofile\schedule_server\configs go run ./scripts/agent_eval -tenant-id 1
```

结果：

- `总样本: 18`
- `领域准确率: 100.0% (18/18)`
- `模式准确率: 100.0% (18/18)`
- `路由准确率: 100.0% (18/18)`
- `知识命中率: 100.0% (10/10)`
- `平均耗时: 141 ms`
- `失败样本: 无`

这轮验证对应的关键改动是：

- `domainGate` 扩充了 `没课 / 实时视图 / 最终结算` 等真实问法
- `hasLiveSignal` 不再把“按谁优先”这类规则问法误判成实时查询
- 纯实时问题即使命中泛知识，也会保留 `tool-first`，不再被误抬成 `mixed`

`2026-03-27` 在 `agent-rag-eval-observability` worktree 中，使用主仓库配置目录执行了一次仅含 `route + retrieval` 的真实验证：

```bash
CONFIG_PATH=G:\gofile\schedule_server\configs go run ./scripts/agent_eval -tenant-id 1
```

结果：

- `总样本: 18`
- `路由准确率: 100.0% (18/18)`
- `知识命中率: 100.0% (10/10)`
- `平均耗时: 86 ms`
- `失败样本: 无`

这轮修正主要覆盖两个根因：

- `query_router` 原先没有识别“区别 / 影响 / 优先级”等规则问法，导致部分规则问题误走 `tool`
- 词法检索原先对中文整句只保留整句词项，容易让所有候选切片同分，退化成按文档顺序返回

同日先补跑了 `with-agent` 模式的预检查：

```bash
CONFIG_PATH=G:\gofile\schedule_server\configs go run ./scripts/agent_eval -tenant-id 1 -with-agent -corp-id dinge292658c9243df4235c2f4657eb6378f -sender-id 01375837500038676039 -sender-name 马华恩
```

当前环境结果：

- 脚本会在启动后给出明确错误：`with-agent 预检查失败: 当前 LLM API Key 看起来是占位值，请先配置可用的 LLM API Key 再运行 with-agent 评测`
- 不再继续跑整批样本并输出一堆由无效 LLM 凭据导致的假阴性 `tools / keywords` 指标

在切换到 `CONFIG_ENV=prod` 后，又执行了一次真实端到端评测：

```bash
CONFIG_PATH=G:\gofile\schedule_server\configs CONFIG_ENV=prod go run ./scripts/agent_eval -tenant-id 1 -with-agent -corp-id dinge292658c9243df4235c2f4657eb6378f -sender-id 01375837500038676039 -sender-name 马华恩
```

结果：

- `总样本: 18`
- `路由准确率: 100.0% (18/18)`
- `知识命中率: 100.0% (10/10)`
- `工具命中率: 100.0% (10/10)`
- `关键词命中率: 91.7% (11/12)`
- `平均耗时: 12467 ms`
- `失败样本: 1`

当前唯一失败样本是：

- `请假同步失败会影响已经生成的考勤快照吗？`

失败维度不是路由、检索或工具调用，而是回复没有完整覆盖样本里设定的全部关键词。这说明当前主链路已经能稳定跑通，剩余问题更像是提示词/答案措辞和评测口径之间的细节偏差。

为确认这不是确定性缺陷，又单独对该样本执行了一次 `prod with-agent` 复跑：

```bash
CONFIG_PATH=G:\gofile\schedule_server\configs CONFIG_ENV=prod go run ./scripts/agent_eval -tenant-id 1 -cases ./tmp-agent-eval-one.json -with-agent -corp-id dinge292658c9243df4235c2f4657eb6378f -sender-id 01375837500038676039 -sender-name 马华恩
```

单样本结果为：

- `路由准确率: 100.0% (1/1)`
- `知识命中率: 100.0% (1/1)`
- `关键词命中率: 100.0% (1/1)`

随后再次执行同一条全量 `prod with-agent` 命令，最新结果为：

- `总样本: 18`
- `路由准确率: 100.0% (18/18)`
- `知识命中率: 100.0% (10/10)`
- `工具命中率: 100.0% (10/10)`
- `关键词命中率: 100.0% (12/12)`
- `平均耗时: 11603 ms`
- `失败样本: 无`

因此，当前更合理的结论是：首轮 `91.7%` 的关键词命中率来自模型回答措辞波动，而不是本地 `route / retrieval / tools` 链路的确定性缺陷。在当前知识库和 `prod.yaml` 配置下，最新一次完整端到端评测已经达到 `100% / 100% / 100% / 100%`。

## 当前限制

- `source_ref` 采用 `文档标题#切片序号`，依赖知识导入时的标题和切片顺序
- 关键词匹配是轻量规则，不是语义判分
- 端到端评测结果会受当前模型、知识库内容和真实业务数据影响
- 当前仓库 `dev.yaml` 中的 `llm.api_key` 是占位值；如果不先替换成有效凭据，`with-agent` 只能做到预检查失败，无法产出真实端到端指标
