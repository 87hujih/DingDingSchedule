# Agent Dataflow

这份文档说明当前 retrieval-first 版本里，知识文档如何从 `docs/agent-knowledge` 进入数据库，以及 `Agent.Chat` 如何先做领域门禁、再检索知识、最后决定回答模式。

对应代码：

- 导入入口：`scripts/sync_agent_knowledge/main.go`
- 切片与同步：`internal/service/agent_knowledge_service.go`
- 仓储层：`internal/repository/agent_knowledge_repository.go`
- 领域门禁与模式决策：`internal/agent/domain_gate.go`、`internal/agent/query_router.go`
- 检索与回答编排：`internal/agent/retrieval.go`、`internal/agent/answer_builder.go`、`internal/agent/agent.go`

## Mermaid

```mermaid
flowchart TD
    A[Markdown 文档<br/>docs/agent-knowledge/*.md] --> B[manifest.yaml<br/>doc_type / audience / intent]
    A --> C[scripts/sync_agent_knowledge/main.go]
    B --> C
    C --> D[MarkdownKnowledgeDocument]
    D --> E[AgentKnowledgeService.SyncMarkdownDocuments]

    E --> F[Upsert 文档元信息<br/>agent_knowledge_documents]
    E --> G[BuildChunks<br/>按 # / ## 标题切片]
    G --> H[chunk 元数据<br/>Heading / Body / SearchText / SourceRef<br/>DocType / Audience / Intent]
    H --> I[ReplaceChunks<br/>写入 agent_knowledge_chunks]

    J[用户问题] --> K[Agent.Chat]
    K --> L[domainGate.Check]
    L -->|out_of_domain| M[直接拒答]
    L -->|in_domain| N[retrieveKnowledge]

    N --> O[AgentKnowledgeService.Search]
    O --> P[Repository.SearchChunks<br/>按 tenant 拉取候选 chunks]
    P --> Q[query normalize / alias 扩展 / intent hint]
    Q --> R[scoreKnowledgeRow + metadata rerank]
    R --> S[topK 命中 + RetrievalResult]

    S --> T[queryRouter.DecideForQuestion]
    T -->|knowledge-only| U[buildKnowledgeOnlyPrompt]
    T -->|mixed| V[buildMixedAnswerPrompt]
    T -->|tool-first| W[保留工具链路]
    T -->|reject| X[返回 noKnowledgeReply]

    U --> Y[LLMClient.Chat<br/>不带 tools]
    V --> Z[LLMClient.Chat<br/>带 tools]
    W --> Z
    M --> AA[最终回复]
    X --> AA
    Y --> AA
    Z --> AA

    AA --> AB[AgentCallLog<br/>domain_result / answer_mode / query_type<br/>retrieval_top_refs / retrieval_scores / knowledge_doc_types]
```

## Plain Text

```text
知识导入
  -> scripts/sync_agent_knowledge/main.go
  -> 读取 docs/agent-knowledge/*.md + manifest.yaml
  -> 生成 MarkdownKnowledgeDocument:
     - Title
     - SourcePath
     - Content
     - Metadata(doc_type / audience / intent)
  -> AgentKnowledgeService.SyncMarkdownDocuments
     -> Upsert 文档元信息到 agent_knowledge_documents
     -> BuildChunks 按 Markdown 标题切片
     -> 为 chunk 生成:
        - Heading
        - Body
        - SearchText
        - SourceRef
        - DocType / Audience / Intent
     -> ReplaceChunks 写入 agent_knowledge_chunks

运行时问答
  -> Agent.Chat
  -> domainGate.Check
     -> out_of_domain: 直接返回职责范围外拒答
     -> in_domain: 进入 retrieval-first 检索

retrieval-first 检索
  -> AgentKnowledgeService.Search
     -> Repository.SearchChunks 按 tenant 拉取候选切片
     -> normalizeSearchText / compactSearchText / splitSearchTerms
     -> alias 扩展真实口语化问法
     -> 按标题 / 正文 / 词项打分
     -> 按 doc_type / audience / intent 轻量重排
     -> 产出 topK hits 与 RetrievalResult

模式决策
  -> queryRouter.DecideForQuestion
     -> knowledge-only: 强知识命中，且没有实时数据需求
     -> mixed: 既有知识命中，又有实时数据需求
     -> tool-first: 主要是实时查询，知识只作为辅助信号
     -> reject: 领域内但没有可用知识，也不适合走工具

回答编排
  -> knowledge-only: buildKnowledgeOnlyPrompt，禁用 tools
  -> mixed: buildMixedAnswerPrompt，要求先给实时结论，再解释规则来源
  -> tool-first: 保留原工具链路
  -> reject: 返回 noKnowledgeReply

调用观测
  -> AgentCallLog
     - domain_result
     - answer_mode
     - query_type(兼容旧口径)
     - retrieval_candidate_count
     - retrieval_top_refs
     - retrieval_scores
     - retrieval_filtered_reason
     - knowledge_doc_types
     - source_refs
     - retrieval_hit_count
     - retrieval_duration_ms
     - llm_duration_ms
```

## One-Line Summary

`Markdown + manifest -> 文档表/切片表 -> in-domain 问题默认先检索 -> 按知识命中和实时信号决定 knowledge-only/tool-first/mixed/reject -> 生成回复并记录完整检索观测`
