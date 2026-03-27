# Agent Dataflow

这份文档说明当前实现里 `Markdown -> documents/chunks -> Search -> Agent prompt` 的数据流。

对应代码：

- 导入入口：`scripts/sync_agent_knowledge/main.go`
- 切片与同步：`internal/service/agent_knowledge_service.go`
- 仓储层：`internal/repository/agent_knowledge_repository.go`
- Agent 分流与注入：`internal/agent/query_router.go`、`internal/agent/retrieval.go`、`internal/agent/agent.go`

## Mermaid

```mermaid
flowchart TD
    A[Markdown 文档<br/>docs/agent-knowledge/*.md] --> B[scripts/sync_agent_knowledge/main.go]
    B --> C[deriveTitle<br/>提取 Title / SourcePath / Content]
    C --> D[MarkdownKnowledgeDocument]
    D --> E[AgentKnowledgeService.SyncMarkdownDocuments]

    E --> F[Upsert 文档元信息<br/>agent_knowledge_documents]
    E --> G[BuildChunks<br/>按 # / ## 标题切片]
    G --> H[生成 chunk 字段<br/>Heading / Body / SearchText / SourceRef]
    H --> I[ReplaceChunks<br/>写入 agent_knowledge_chunks]

    J[用户问题] --> K[Agent.Chat]
    K --> L[queryRouter.Route]
    L -->|tool| M[直接走 tools]
    L -->|rag| N[retrieveKnowledge]
    L -->|mixed| N

    N --> O[AgentKnowledgeService.Search]
    O --> P[Repository.SearchChunks<br/>按 tenant 拉取候选 chunks]
    P --> Q[normalizeSearchText / compactSearchText / splitSearchTerms]
    Q --> R[scoreKnowledgeRow<br/>标题 / 正文 / 词项打分]
    R --> S[排序 + topK 命中]

    S --> T[buildKnowledgePrompt]
    T --> U[拼成 system prompt<br/>来源 + 标题 + 小节 + 内容]

    U -->|rag| V[LLMClient.Chat<br/>不带 tools]
    U -->|mixed| W[LLMClient.Chat<br/>带 tools]
    M --> W

    V --> X[最终回复]
    W --> X

    X --> Y[AgentCallLog<br/>query_type / source_refs / retrieval_hit_count / llm_duration_ms]
```

## Plain Text

```text
Markdown 文档
  -> scripts/sync_agent_knowledge/main.go
  -> deriveTitle 提取 Title / SourcePath / Content
  -> MarkdownKnowledgeDocument
  -> AgentKnowledgeService.SyncMarkdownDocuments
     -> Upsert 文档元信息到 agent_knowledge_documents
     -> BuildChunks 按 Markdown 标题切片
     -> 生成 chunk 字段:
        - Heading
        - Body
        - SearchText
        - SourceRef
     -> ReplaceChunks 写入 agent_knowledge_chunks

用户问题
  -> Agent.Chat
  -> queryRouter.Route
     -> tool  : 直接走 tools
     -> rag   : 走 retrieveKnowledge
     -> mixed : 走 retrieveKnowledge + tools

retrieveKnowledge
  -> AgentKnowledgeService.Search
     -> Repository.SearchChunks 按 tenant 拉取候选切片
     -> normalizeSearchText
     -> compactSearchText
     -> splitSearchTerms
     -> scoreKnowledgeRow
     -> 排序后取 topK

topK 命中切片
  -> buildKnowledgePrompt
  -> 生成 system prompt:
     - 来源
     - 标题
     - 小节
     - 内容

system prompt
  -> rag   : LLMClient.Chat，不带 tools
  -> mixed : LLMClient.Chat，保留 tools

最终回复
  -> AgentCallLog
     - query_type
     - source_refs
     - retrieval_hit_count
     - retrieval_duration_ms
     - llm_duration_ms
```

## One-Line Summary

`Markdown 文档 -> 同步脚本 -> 文档表/切片表 -> Search 打分取 topK -> 拼成知识 prompt -> Agent 按 rag/mixed 注入给模型 -> 生成回复并记录日志`
