# AGENTS.md

## 项目背景

本项目是(LocalRelay)一个**纯本地运行的大模型（LLM）中转/网关桌面软件**，定位类似 NewAPI / OneAPI，但目标是"打开即用"的本地 GUI 软件，不依赖 Docker、不依赖额外运行时环境。

### 核心目标

- 统一管理多个 LLM 提供商（Provider）及其下模型（Model）的配置。
- 对外以统一接口转发请求到各提供商，支持以 `providerId/modelId` 的形式路由到具体模型。
- 对外规划支持四种主流协议入口：**OpenAI Chat Completions**、**OpenAI Responses**、**Anthropic Messages**、**Google Gemini**。当前仅实现 **OpenAI Chat Completions** 入站（`/v1/chat/completions`、`/v1/models`、`/healthz`），其余三种入站解析在 ROADMAP 计划中；出站（IR → 上游）四种协议均已实现。
- 记录每次调用日志，支持按时间区间统计 Token 使用（输入/输出分别统计，支持缓存 Token 区分，视提供商能力而定）。上游成功响应但未返回 usage 时，网关按请求与响应内容本地估算 Token 并在日志/统计中标记 `token_estimated`，上游返回的 usage 始终优先。
- 提供美观易用、指引明确的本地管理界面。

### 架构核心思路

- 采用**内部统一格式（IR，Intermediate Representation）为中心的星形转换**，而非四种协议两两互转的网状转换：所有外部协议先转换为内部格式，再由内部格式转换为目标上游协议；对外响应同理，先转换为内部格式，再转换为对外协议格式。
- 内部格式设计以 **Anthropic Messages 结构为蓝本**做适度扩展（block 化的 content 表达能力最强，其他协议可视为其降级映射）。
- 流式（SSE）与非流式的内部格式**分别设计**，不假设可以互相简单派生。
- 不同提供商在同一协议类型下的细节差异（思考开关字段、reasoning_effort 取值、思考内容是否回传、cache token 字段命名、流式是否需要 `stream_options.include_usage` 等）通过**可配置的"能力描述 + 适配器"层**解决，不硬编码 if-else，新增/调整某个提供商的怪异字段应尽量只改配置，不改核心转换逻辑。能力配置结构见 `internal/capabilities`（`Provider` 含 `Protocol` / `Thinking` / `ReasoningEffort` / `ToolCalls` / `Streaming`）。
- 优先实现「OpenAI Chat → 内部格式」与「内部格式 → 各上游协议」，再逐步扩展「OpenAI Response / Anthropic / Google → 内部格式」。

## 技术栈约束

- 桌面壳与后端服务：**Go + Wails**。
  - 后端服务逻辑、协议转换、HTTP/SSE 处理均使用 Go 编写。
  - 优先考虑 Wails v2。
- 前端 UI：React + TailwindCSS，组件库参考 shadcn 风格。
- 数据存储：SQLite，优先使用纯 Go 驱动（如 `modernc.org/sqlite`），避免 CGO 依赖以简化 Windows 打包。
- 图表：ECharts 或同等前端图表库，用于 Token 统计可视化。
- CI/CD：GitHub Actions，产物发布到 GitHub Releases，用于配合自动更新机制。

## 开发原则与约束

- **配置优先于硬编码**：涉及不同提供商差异的字段、开关、取值方式，必须落在可配置的能力描述层，不允许写成分散在业务逻辑中的 if-else 判断。
- **协议转换保真度优先**：内部格式与各协议互转时，优先保证信息不丢失（尤其是 thinking block、多个 tool_use 混排等场景），若某协议表达能力不足必须降级，需要在代码注释中显式说明降级点。
- **流式与非流式路径分离实现和测试**，不得假设一套逻辑可覆盖两种场景。
- **敏感信息处理**：API Key 等敏感配置在本地存储时必须加密（如 AES + 本机绑定），禁止以明文形式落盘。
- **最小可用优先**：新功能按"先跑通端到端最小路径，再扩展兼容范围"的顺序实现，不追求一次性覆盖所有提供商/协议组合。
- **模块边界清晰**：协议解析层、内部格式、上游适配器、存储层、统计层应保持职责分离，避免跨层直接耦合具体供应商细节。
- **测试覆盖**：所有功能必须有对应测试，排除明确不需要测试的代码（如 UI 入口、纯生成代码、第三方绑定层等）后，总体测试覆盖率需达到 **90% 以上**。流式与非流式、各协议适配器的 `ToInternal` / `FromInternal` 均需有独立测试用例。
- **ROADMAP 由人工维护**：完成任务时**不得自动**将 `ROADMAP.md` 中的任务从"进行中/待办"移动到"已完成"分区，这一步由用户手动操作。Agent 仅在对话中提示该任务已可标记完成即可。

## 代码规范

### Go 后端

- 遵循标准 Go 项目约定，使用 `gofmt` / `goimports` 格式化，提交前必须通过 `go vet`。
- 错误处理使用显式 `error` 返回，禁止吞掉错误或用 panic 代替正常错误流程（除非是不可恢复的初始化错误）。
- 涉及协议字段映射的结构体，字段命名与 JSON tag 需清晰标注对应的外部协议字段，避免"神秘字段"。
- 各协议适配器应实现统一接口（IR ↔ 上游协议的 `ToProviderRequest` / `ParseResponse` / `FromIRResponse*` / `WriteStreamEvent` 等），新增供应商类型或预设时优先通过新增配置/实现文件完成，不修改已有适配器核心逻辑。
- 日志、统计相关代码需保证不因单次调用失败而影响主流程稳定性（如统计写入失败不应导致请求失败）。

### 前端

- 使用 TailwindCSS 工具类，避免内联样式；组件保持单一职责，表单类组件优先由配置（JSON Schema 风格）驱动生成，而非为每个提供商类型手写重复表单。
- 涉及供应商/模型管理、Token 统计等页面，需保证操作路径清晰（如新增/编辑/删除有明确反馈，统计筛选条件有默认合理值）。

### 通用

- 所有跨协议、跨供应商的差异点在实现时应有对应注释或文档说明依据（来源于官方文档或已验证的实际行为）。
- 提交前自测覆盖：至少验证一次端到端的真实请求（非纯单元测试通过），涉及流式响应的改动需实际验证流式输出的完整性和分块正确性。

## Git 提交消息规范

### 格式

```
<emoji> <type>(<scope>): <subject>

<body>

<footer>
```

- **emoji**：视觉分类标识，必须使用
- **type**：`feat` / `fix` / `refactor` / `docs` / `test` / `chore` / `style` / `perf`
- **scope**：可选，如 `(opds)`、`(spider)`、`(api)`、`(web)`
- **subject**：中文标题，概括变更内容，首字无需空格
- **body**：英文或中英文混排，每行为一个 `- ` 开头的条目，描述具体变更
- **footer**：可选的 `Refs:` 或 `BREAKING CHANGE:`

### Emoji 对照表

| Type       | Emoji | 含义                                    |
| ---------- | ----- | --------------------------------------- |
| `feat`     | ✨    | 新功能                                  |
| `fix`      | 🐛    | Bug 修复                                |
| `refactor` | ♻️    | 代码重构                                |
| `docs`     | 📚    | 文档变更                                |
| `test`     | 🧪    | 测试相关                                |
| `chore`    | 🔧    | 工程化/依赖/配置                        |
| `style`    | 🎨    | 代码格式/样式                           |
| `perf`     | ⚡    | 性能优化                                |
| `wip`      | 🚧    | 进行中（仅临时使用，合并前必须 squash） |

### 示例

```
✨ feat(opds): 实现 OPDS 基础层——可见性控制与 EPUB 制品生命周期

- DB: add opds_visible, content_updated_at, epub_compiled_at columns
- Repository: add OPDS CRUD methods
- OpdsCompilationService: new cron-based scheduler

Refs: ROADMAP OPDS 书源服务构建与分发
```

```
🐛 fix(api): 修复定时更新策略变更后调度器未正确重载的并发问题
```

```
📚 docs: 添加 OPDS 书源服务任务到路线图
```

### 约定

- 多条变更在同一提交中时，`subject` 概括主要变更，`body` 逐条列举
- 每行 body 以 `- ` 开头，长度不超过 72 字符（英文）或适当截断
- **禁止**仅重复文件列表而无语义描述的提交
- **禁止**在提交消息中包含内部指令或占位符（如 "TODO"、"TBD"）
