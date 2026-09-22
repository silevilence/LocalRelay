# AGENTS.md

## 项目背景

本项目是(LocalRelay)一个**纯本地运行的大模型（LLM）中转/网关桌面软件**，定位类似 NewAPI / OneAPI，但目标是"打开即用"的本地 GUI 软件，不依赖 Docker、不依赖额外运行时环境。

### 核心目标

- 统一管理多个 LLM 提供商（Provider）及其下模型（Model）的配置。
- 对外以统一接口转发请求到各提供商，支持以 `providerId/modelId` 的形式路由到具体模型。
- 对外支持四种主流协议入口：**OpenAI Chat Completions**（`/v1/chat/completions`、`/v1/models`、`/healthz`）、**Anthropic Messages**（`/v1/messages`）、**OpenAI Responses**（`/v1/responses`）、**Google Gemini**（`/v1beta/models/*:generateContent`），四种协议的入站解析与出站（IR → 上游）转换均已实现。
- 记录每次调用日志，支持按时间区间统计 Token 使用（输入/输出分别统计，支持缓存 Token 区分，视提供商能力而定）。上游成功响应但未返回 usage 时，网关按请求与响应内容本地估算 Token 并在日志/统计中标记 `token_estimated`，上游返回的 usage 始终优先。四种入站协议按各自习惯识别 API Key 并写入统计「应用」列（OpenAI Chat/Responses 用 `Authorization: Bearer`；Anthropic 用 `x-api-key`；Gemini 用 `x-goog-api-key` 或 query `key`），识别规则与 OpenAI Chat 入站保持一致。
- 客户端请求头**默认透传**到上游（黑名单排除，而非白名单匹配）：自定义会话/幂等/追踪类头无需改代码即可到达上游；逐跳头、`Cookie`、客户端凭据与不适用于重写后请求体的编码头会被剔除，发往上游的认证头与 `Content-Type` 始终由网关按当前供应商重写。
- 提供美观易用、指引明确的本地管理界面。

### 架构核心思路

- 采用**内部统一格式（IR，Intermediate Representation）为中心的星形转换**，而非四种协议两两互转的网状转换：所有外部协议先转换为内部格式，再由内部格式转换为目标上游协议；对外响应同理，先转换为内部格式，再转换为对外协议格式。
- 内部格式设计以 **Anthropic Messages 结构为蓝本**做适度扩展（block 化的 content 表达能力最强，其他协议可视为其降级映射）。
- 流式（SSE）与非流式的内部格式**分别设计**，不假设可以互相简单派生。
- 不同提供商在同一协议类型下的细节差异（思考开关字段、reasoning_effort 取值、思考内容是否回传、cache token 字段命名、流式是否需要 `stream_options.include_usage` 等）通过**可配置的"能力描述 + 适配器"层**解决，不硬编码 if-else，新增/调整某个提供商的怪异字段应尽量只改配置，不改核心转换逻辑。能力配置结构见 `internal/capabilities`（`Provider` 含 `Protocol` / `Thinking` / `ReasoningEffort` / `ToolCalls` / `Streaming`）。
- 能力规则可细化到**模型级**：模型的能力 JSON 可携带 `providerCapabilityOverrides`（结构与供应商能力配置一致，深合并覆盖，如 reasoning_content 回传、requireAssistantContent、thinking 字段等）与模型级 `allowThinkingDowngrade` 布尔策略（请求含工具调用但历史缺失 reasoning_content 时自动关闭思考模式继续请求，默认关闭），请求时经 `capabilities.ApplyModel` 与供应商能力合并后再进入协议转换，`AllowThinkingDowngrade` 被排除在供应商 JSON 之外、只能模型级显式开启。模型能力写入前须经 `capabilities.ValidateModel` 校验；新增预设或迁移（如 store 迁移 v10 为 Opencode GO 的 DeepSeek V4 模型补齐配置）必须与此机制一致，禁止绕过校验直接写库。

### 客户端请求头透传与安全边界

- 出站请求头由 `internal/relay/headers.go` 的 `upstreamHeaders` 构造：**默认透传全部客户端请求头**，采用黑名单排除。入站侧在 `handleClientRequest` 中以 `r.Header.Clone()` 快照一次，随 `inboundRequest.clientHeaders` 传给 `postProvider`；直连与聚合路由（`forwardAggregation`）共用同一份快照，聚合主备重试仍携带同一会话标识。`postProvider` 是 relay 转发链路上唯一的上游请求构造点，直连与聚合、流式与非流式均经它出站（`app.go` 的供应商连通性测试为应用自发请求，不经客户端头透传）。
- 排除清单为**包级只读常量** `excludedClientHeaders`：逐跳头（`Connection` / `Keep-Alive` / `Proxy-Connection` / `TE` / `Trailer` / `Transfer-Encoding` / `Upgrade` / `Proxy-Authenticate` / `Proxy-Authorization`）、`Cookie`、`Host`、`Content-Length`、`Content-Encoding`、`Expect`、`Accept-Encoding`，以及客户端凭据与协议头 `Authorization` / `X-Api-Key` / `X-Goog-Api-Key` / `Anthropic-Version` / `Content-Type`；另按 RFC 9110 §7.6.1 额外排除 `Connection` 头中列出的字段名（该动态部分必须留在函数局部，禁止写入包级常量，否则会污染其他请求的过滤结果）。
- 排除依据：请求体经 IR 转换后由 `json.Marshal` 重新序列化为未压缩 JSON，入站 `Content-Encoding` / `Expect` / `Content-Length` 不再描述该请求体；转发客户端 `Accept-Encoding` 会关闭 Go Transport 的自动解压，破坏压缩 JSON/SSE 的解析。新增敏感头时必须同步加入排除清单。
- 上游请求的 `Content-Type: application/json` 与认证头由 `postProvider` **无条件重写**（Anthropic 另写 `Anthropic-Version: 2023-06-01`）；未配置上游 API Key 时同样先删除入站凭据，禁止把客户端凭据转发给第三方上游。

### 供应商启用开关与路由错误

- `providers` 表含 `enabled` 列（schema 迁移 v11 `ensureProviderEnabledColumn`，`ALTER TABLE ... NOT NULL DEFAULT 1`，历史数据视为启用）。`Provider.Enabled` 是该状态的唯一读出口，`ListProviders` / `GetRoutedModel` 的查询列必须同步携带该字段；迁移必须走版本化迁移表，禁止在业务路径里 `ALTER TABLE`。
- 开关的独立翻转走 `Store.SetProviderEnabled(id, enabled)`（Wails 绑定 `App.SetProviderEnabled`，前端列表徽标与详情页开关都调用它），它会刷新 `updated_at`，id 不存在时返回 `sql.ErrNoRows`。
- `ProviderInput.Enabled` 是 `*bool`，**nil 语义为"不改变"**：新建时经 `enabledOrTrue` 默认启用，编辑时由 `UPDATE ... enabled = COALESCE(?, enabled)` 在 SQL 内保留原状态，避免"表单保存"与"开关翻转"并发时相互覆盖。前端平台编辑表单（`providerToDraft`）刻意不回传该字段，新增表单字段时应保持这一约定。
- 路由错误映射集中在 `internal/relay/server.go` 的 `routeError`（同时给状态码与错误码，二者不得分散在两个函数里各写一遍 switch）：供应商被停用时 `GetRoutedModel` 返回 `store.ErrProviderDisabled` → 400 `provider_disabled`；模型 id 非法 / 模型停用 → 400；模型不存在 → 404 `model_not_found`。四种入站协议与流式/非流式共用该路径，被停用平台不会收到任何上游请求。
- `/v1/models` 由 `ListEnabledModels` 提供，条件为 `m.enabled = 1 AND p.enabled = 1`，因此停用平台会隐藏其全部模型。
- 聚合：成员解析阶段 `GetRoutedModel` 失败（含平台被停用）只记入 `log.AggAttempts`，不得进入候选集合，也不得写冷却期；候选为空时返回「聚合没有可用成员」（502）。聚合 Provider 自身被停用时同样返回 `provider_disabled`。
- 前端：列表徽标与详情页开关反映真实状态，翻转期间所有开关按 `providerSavingId` 置为 disabled 以防并发；平台表单草稿的同步依赖 `providerToDraft` 投影出的 JSON 而不是 `provider` 对象，后台刷新（启用状态、`updated_at` 变化）不会覆盖未保存的表单内容。

### 流式超时语义

- 流式上游请求必须走 `internal/relay/timeout.go` 的 `doStreamRequest`，**禁止**直接用 `s.client.Do`：`http.Client.Timeout` 覆盖响应体读取，会把健康的长 SSE 流在固定总时限处截断。
- 语义：同一份 2 分钟预算先用于等待上游响应头，收到响应头后重置为读空闲超时；`streamTimeoutBody.Read` 读到任意字节（含 SSE 注释心跳与尚未拼完的事件）都刷新计时。非流式请求保持 `client.Timeout` 的 2 分钟总时限。
- 超时经 `context.WithCancelCause` 记录原因（`upstream response timeout` / `upstream stream idle timeout`），请求结束必须 `finish()` 释放计时器与子 context，避免把正常 EOF 记成 `context canceled`；客户端取消与聚合主备的成员尝试时限仍由 request context 生效。

### 桌面集成与平台分层

- 桌面集成（系统托盘、开机启动、窗口最小化/关闭隐藏、启动隐藏、网关服务启停）通过 **Go build tag** 分平台实现：`desktop_windows.go`（`//go:build windows`）承载 Windows 完整实现，`desktop_other.go`（`//go:build !windows`）提供同名方法的空实现/不支持错误，确保非 Windows 构建不崩溃。新增平台集成能力必须同时补齐 `desktop_other.go` 的占位，禁止让非目标平台编译失败。
- 桌面相关开关（`GatewayEnabled` / `HideOnMinimize` / `HideOnClose` / `LaunchAtLogin` / `StartMinimized`）持久化在 `app_settings` 表，统一通过 `internal/store.DesktopSettings` 读写；新增桌面开关应扩展该结构体并补默认值，不要新建独立的存储表。前端绑定集中在 `app_desktop.go`。
- 网关服务启停（`SetRelayServiceEnabled`）只关闭 `http.Server` 监听，**不销毁** `relay.Server` 实例与其已加载的存储，便于再次开启时无需重新初始化；关闭走 `Shutdown`（3 秒超时）后再 `Close` 兜底，保证持久化的禁用态与实际监听态一致。
- 系统托盘菜单项（如「暂停/恢复网关服务」）必须与设置页开关**双向同步**：托盘操作翻转状态后通过 `runtime.EventsEmit` 通知前端，前端翻转后调用 `updateTrayGatewayMenu` 同步托盘菜单文字。
- 更新安装（`InstallUpdate` → `launchUpdateInstaller`）启动安装包后必须调用 `App.RequestQuit()`（置 `quitting` 标志后再 `runtime.Quit`），**不得**直接用 `wailsruntime.Quit`：它同样触发 `beforeClose`，会被「关闭时隐藏到托盘」拦截，导致安装程序一直等待本进程退出。安装包启动失败时保持应用运行，不得退出。

## 技术栈约束

- 桌面壳与后端服务：**Go + Wails**。
  - 后端服务逻辑、协议转换、HTTP/SSE 处理均使用 Go 编写。
  - 优先考虑 Wails v2。
- 前端 UI：React + TailwindCSS，组件库参考 shadcn 风格。
- 数据存储：SQLite，优先使用纯 Go 驱动（如 `modernc.org/sqlite`），避免 CGO 依赖以简化 Windows 打包。
- 图表：ECharts 或同等前端图表库，用于 Token 统计可视化。
- 系统托盘：使用纯 Go 的 `github.com/energye/systray`（无 CGO），Windows 通知使用 `git.sr.ht/~jackmordaunt/go-toast/v2`；跨平台扩展时优先沿用纯 Go 方案，避免引入 CGO。
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

- 遵循标准 Go 项目约定，使用 `gofmt` / `goimports` 格式化，提交前必须通过 `go vet`。本地全量验证依次为 `gofmt`、`go vet ./...`、`go test ./...`，测试文件与被测代码同目录。
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
- **scope**：可选，如 `(relay)`、`(protocol)`、`(app)`、`(desktop)`、`(store)`、`(updater)`、`(ci)`
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
✨ feat(desktop): 实现系统托盘、开机启动与网关服务开关

- tray: 新增 systray 托盘图标，右键菜单支持显示窗口/暂停恢复网关/退出
- window: 最小化与关闭可隐藏到托盘，支持启动时自动隐藏
- store: 新增 DesktopSettings 持久化桌面相关开关

Refs: ROADMAP 系统功能增强
```

```
🐛 fix(relay): 修复流式响应尾包 Token 用量未写入统计的问题
```

```
📚 docs: 更新 README 与 AGENTS 文档同步桌面集成能力
```

### 约定

- 多条变更在同一提交中时，`subject` 概括主要变更，`body` 逐条列举
- 每行 body 以 `- ` 开头，长度不超过 72 字符（英文）或适当截断
- **禁止**仅重复文件列表而无语义描述的提交
- **禁止**在提交消息中包含内部指令或占位符（如 "TODO"、"TBD"）
