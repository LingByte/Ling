# Ling 升级路线图（面向强 AI 框架）

本文档给出一个实战导向的升级清单：把当前 Ling 从“可用组件集合”提升到“可扩展、可观测、可评估、可运营”的 AI 框架。

## 目标定义（你要成为哪一类框架）

建议把目标定为：

- **编排层强**：像 LangChain/LangGraph，可声明式编排、可分支、可重试、可并行。
- **RAG 强**：可插拔向量库/重排/过滤/多路召回，支持评估与自动回归。
- **Agent 强**：计划、执行、反思、工具调用、长期记忆都标准化。
- **工程化强**：可观测、可压测、可灰度、可回滚、可运营。

## 当前优势（你已有的基础）

- 有完整模块：`parser/chunk/extract/retrieval/knowledge/rewrite/expand/compress/censor/agent`。
- 已有责任链能力：`pkg/chain` + 新增 `pkg/pipeline`（Step/Builder/Router/Retry）。
- 已有多 LLM provider 入口与 RAG 主路径。
- 有服务化入口与可视化管理页面雏形（`cmd/server`）。

## 关键能力缺口（按优先级）

### P0（必须先补）

- **统一运行时协议**
  - 现状：组件接口风格不完全一致。
  - 建议：统一 `Runnable` 抽象（`Invoke/Stream/Batch`）+ 标准 `State` + `Context`。

- **可观测性**
  - 现状：有部分 usage 信号，但未形成端到端 trace。
  - 建议：统一 TraceID，记录每个 step 的输入摘要、输出摘要、耗时、token、错误类型。

- **评估体系（Eval）**
  - 现状：有测试但缺“答案质量评测”。
  - 建议：建立评测数据集 + 自动评分（命中率、忠实性、上下文利用率、拒答准确率）。

- **配置治理**
  - 现状：环境变量较散，默认值和运行时策略耦合。
  - 建议：统一 `Config Schema` + 启动校验 + provider profile（dev/staging/prod）。

### P1（强烈建议尽快）

- **并行与异步编排**
  - 新增 `ParallelStep`、`MapStep`、`JoinStep`，支持多路召回并行后融合。

- **Memory 系统**
  - 短期会话记忆 + 长期偏好记忆 + 摘要记忆，支持 TTL、命中策略与隐私擦除。

- **工具调用治理**
  - Tool schema 版本化、权限边界、超时与熔断、失败回退策略统一。

- **Prompt/Output 标准层**
  - Prompt 模板管理（版本、变量校验），输出 parser（JSON schema 强校验 + 自动修复）。

### P2（拉开差距）

- **多 Agent 协作**
  - Planner / Retriever / Writer / Critic 的多角色图编排。

- **知识库治理**
  - 文档版本、增量更新、冲突合并、软删除、冷热分层、索引重建策略。

- **企业级能力**
  - 多租户隔离、RBAC、审计日志、策略引擎、成本预算控制。

## 架构优化建议（你的项目可直接落地）

### 1) 统一运行时内核

- 在 `pkg/pipeline` 增加：
  - `ParallelStep`
  - `ConditionalStep`
  - `TimeoutStep`
  - `CircuitBreakerStep`
  - `FallbackStep`
- 让 `pkg/chain` 仅作为“预置业务链集合”，底层全部走 `pipeline`。

### 2) 标准化上下文对象

定义统一 `ExecutionContext`：

- `TraceID`, `SessionID`, `UserID`
- `Budget`（token/time/cost）
- `MemoryRef`
- `FeatureFlags`

### 3) RAG 检索升级

- 多路召回：向量、关键词、结构化过滤并行。
- 融合策略可插拔：`weighted`, `rrf`, `learning-to-rank`。
- 重排链路可观测：记录重排前后 topK 变化。

### 4) Agent 执行器升级

- 从“重试任务”升级为“可恢复状态机”：
  - `planned -> running -> waiting_tool -> retrying -> done/failed`
- 支持 checkpoint，任务中断后可恢复。

## 指标体系（没有指标就没有升级）

至少要看这 5 类指标：

- **质量**：准确率、忠实性、拒答准确率、人工偏好分。
- **检索**：Recall@K、MRR、重排提升率。
- **效率**：P50/P95 延迟、首 token 时延、吞吐。
- **成本**：每请求 token、每请求费用、重试开销占比。
- **稳定性**：错误率、超时率、降级触发率。

## 三阶段落地计划（建议）

### 阶段一（1-2 周）

- 完成 `pipeline` 的并行/条件/超时/回退步骤。
- 给 `cmd/server` 增加链路调试视图（显示每步耗时和输出摘要）。
- 建立最小评测集（20-50 条）+ nightly 回归。

### 阶段二（2-4 周）

- 完成 memory 模块与会话管理。
- 完成 tool calling 治理层（权限、超时、审计）。
- 完成多路召回融合策略可配置化。

### 阶段三（4-8 周）

- 完成多 Agent 图编排。
- 完成多租户、配额与成本治理。
- 完成线上观测看板（质量/成本/稳定性三维）。

## 下一步可直接做的 10 个任务（可执行）

- [ ] 在 `pkg/pipeline` 新增 `ParallelStep` 与 `FallbackStep`
- [ ] 给 `chain` 增加统一 `StepResult`（tokens/cost/latency/error）
- [ ] 给 `cmd/server` 增加链路运行日志页
- [ ] 增加 `rag_eval` 命令（离线评测）
- [ ] 为 `knowledge` 增加 provider profile 配置文件
- [ ] 引入 query rewrite A/B 开关
- [ ] 给 rerank 增加开关与阈值配置
- [ ] 增加 embedding 缓存（key: content hash）
- [ ] 增加结果引用（answer 引用 chunk id）
- [ ] 增加故障注入测试（超时、429、空返回、脏 JSON）

## 结论

你现在已经有了“框架雏形”和正确方向。真正把项目做成“厉害的 AI 框架”的关键，不是再堆功能，而是：

- 统一运行时协议
- 建立可观测与评估闭环
- 把编排、检索、Agent、治理四块做成可配置基础设施

当这四块打通后，Ling 会从“组件可用”跃迁到“平台可持续演进”。

