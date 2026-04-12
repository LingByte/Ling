# LLM 会话压缩与 Chain 能力汇总（代码扫描，2026-04-12）

本文基于当前仓库源码整理，回答两件事：（1）多轮会话是否在超过一定量后自动「压缩」；（2）是否已有可组合的 Chain，以及自查询链式示例入口。

---

## 1. 会话记忆：是否自动压缩？是否异步？并发是否安全？

### 1.1 结论概览

| 实现 | 自动触发「压缩」 | 机制说明 |
|------|------------------|----------|
| `pkg/llm` 中的 **`OpenaiHandler`**（含 `newOpenAICompatibleHandler` 走到的同一套逻辑） | **是** | 当内存中的对话轮数达到 `maxMemoryMessages`（默认 **40** 条 `ChatCompletionMessage`，即用户/助手消息条数之和）且当前没有在跑摘要时，会在 **`Query` / `QueryStream` 成功后** 异步调用一次「对话摘要」接口；摘要写回后，用 `summary` 系统消息替代较早内容，并 **`compactMessagesKeepNewer` 丢弃已摘要的旧轮次**（保留较新片段）。 |
| **Ollama / LM Studio / Anthropic / Coze / Alibaba** | **是（与 OpenAI 同语义）** | 使用 `pkg/llm/async_turn_memory.go`：当 `turns` 条数达到 `maxMemoryMessages`（默认 40）时，在 `Query` / `QueryStream` 成功后 **异步** 调用一次「对话摘要」；写回 `summary` 后丢弃已摘要的旧轮次。摘要实现：前三个走 **Chat Completions** 兼容 HTTP；Alibaba 走 **Apps completion** 文本 prompt；Coze 使用 **独立 UserID 后缀** 的单轮 Chat 调用 Bot 生成摘要（不污染主会话消息）。 |

与 RAG 无关的 **`pkg/compress`** 是对「检索到的知识块」做压缩，不是多轮 Chat 记忆压缩。

### 1.2 异步与并发安全

- **`OpenaiHandler`**：`startAsyncSummarizeIfNeeded` + `go func()` + `sync.Mutex` + `atomic.Bool` + `summarizeSeq`，语义见上表。
- **`asyncTurnMemory`（Ollama / LM Studio / Anthropic / Coze / Alibaba）**：同一套 **mutex + `summarizing` + `summarizeSeq` + 快照 goroutine**；触发点在每次成功追加 user/assistant 后对 `len(turns) >= maxMemoryMessages` 判断。
- **残留风险**：摘要基于快照，属**最终一致**；若业务要求「摘要完成前禁止继续对话」需上层串行化。

可调接口：`SetMaxMemoryMessages` / `GetMaxMemoryMessages`；手动同步摘要：`SummarizeMemory`（与自动摘要共享 `summarizing` / `summarizeSeq` 协调）。

`LLMOptions.Logger`（`*zap.Logger`）可选，用于异步摘要失败等日志。

`QueryOptions.EmotionalTone`：为 true 时在 system/指令侧追加一段「略偏情感化、有温度」的中文风格说明（各 Provider 已接入）。`chain.AnswerStep.EmotionalTone` 会传给 `QueryWithOptions`；demo 用环境变量 `LING_EMOTIONAL=1` 开启。

---

## 2. Chain：是否已有链接能力？示例命令在哪？

### 2.1 已有实现

- **`pkg/chain`**：`Chain` 包装 **`pkg/pipeline`**，按步骤对共享 **`chain.State`** 顺序执行。
- **`pkg/chain/steps.go`** 已提供：`SelfQueryStep`、`ExpandStep`、`RewriteStep`、`RetrieveStep`、`CompressStep`、`AnswerStep` 等。
- **`pkg/selfquery`**：从自然语言问题抽取 **检索 query + 结构化 filters**（JSON），与向量检索配合使用；filters 在仅跑链、不接 Qdrant 时仍可打印作调试。

### 2.2 自查询 → 扩展 → 重写 → RAG（Hybrid + 可选 rerank）→ 压缩 → LLM（示例）

```bash
# 完整 RAG：需 Qdrant、Embedding、本地索引目录 LING_HYBRID_INDEX_BASE；可选 SiliconFlow rerank
go run ./cmd/chain_selfquery_demo "你的问题"

# 仅前半段（无向量库时）
go run ./cmd/chain_selfquery_demo --no-rag "你的问题"
# 或 export LING_SKIP_RAG=1
```

链步骤：**Self-Query → Expand → Rewrite → Retrieve（合并 Self-Query filters）→ Rule Compress → Answer（默认 RAG 提示词）**。Hybrid 内部在配置 `Reranker` 时会做 **rerank 重排**。

### 2.3 为支持上述链对代码做的少量扩展（便于复用）

- **`chain.State`**：增加 `SelfQueryText`、`SelfQueryFilters`。
- **`ExpandStep`**：`UseSelfQuery` 为真时优先用 `SelfQueryText` 作为扩展输入。
- **`RewriteStep`**：`UseExpanded` 为真时用 `Expanded` 作为重写输入。
- **`RetrieveStep`**：`UseSelfQueryString` / `UseSelfQueryFilters` 将自查询结果用于检索与 Qdrant filter 合并（`retrieval.MergeQdrantFilters`）。
- **`AnswerStep`**：`AllowWithoutRetrieve` + `BuildPrompt` 可在无检索时仍调 LLM（`--no-rag` 路径）。`RelaxContextOnly` 为真时，默认提示词在「无资料 / 资料与问题无关」时允许用模型常识或创作作答；设 `LING_STRICT_RAG=1` 可恢复严格仅资料模式。

架构示意见 `docs/architecture.md`。

---

## 3. 关键源码锚点（便于跳转）

- 默认条数、异步摘要与锁：`pkg/llm/openai.go`（`defaultMaxMemoryMessages`、`startAsyncSummarizeIfNeeded`、`Query` / `QueryStream` 尾部）。
- 其它 Provider 共享记忆：`pkg/llm/async_turn_memory.go`。
- Chain 定义与 `Run`：`pkg/chain/chain.go`。
- 步骤实现：`pkg/chain/steps.go`。
- 自查询抽取：`pkg/selfquery/extractor.go`。
- Filter 合并：`pkg/retrieval/hybrid.go`（`MergeQdrantFilters`）。
