# LocalRelay

纯本地运行的大模型（LLM）中转/网关桌面软件，打开即用，无需 Docker 或额外运行时环境。

## 功能

- **多供应商统一管理**：在界面中添加、编辑、删除多个 LLM 供应商及其模型，内置 DeepSeek、火山引擎 Coding Plan、Opencode GO、硅基流动、Anthropic、Google Gemini、OpenAI Responses 等常用预设，一键导入。
- **统一模型路由**：对外以 `供应商ID/模型ID` 形式路由到具体模型，可自由设置对外提供的模型范围。
- **平台启用开关**：每个供应商（平台）可在列表徽标或详情页开关一键启用/停用，停用只影响对外服务、不删除任何配置；停用后其下模型不再被路由（调用返回明确的「平台已禁用」错误），也不再出现在 `/v1/models` 列表中。
- **聚合路由**：可创建无上游凭据的聚合 Provider，并以主备、轮询、按 Token 均衡或分时策略路由到真实成员模型；主备在连接、超时、HTTP/解析失败时会在首个 SSE 事件前切换备成员。
- **多协议出站适配**：对上游支持 OpenAI Chat、Anthropic Messages、Google Gemini、OpenAI Responses 四种协议，按各供应商实际接口自动转换。
- **客户端请求头透传**：客户端携带的自定义请求头（会话标识、幂等键、追踪标识等）默认原样转发到上游，便于上游按会话归并上下文、去重与限流；直连与聚合路由行为一致，主备切换重试仍携带同一会话标识。逐跳头、`Cookie`、客户端凭据以及与实际请求体不符的编码声明会被自动剔除，发往上游的认证头与 `Content-Type` 始终由网关按当前供应商重写。
- **四协议入站**：对外同时暴露 OpenAI Chat Completions（`/v1/chat/completions`、`/v1/models`，另有 `/healthz` 健康检查）、Anthropic Messages（`/v1/messages`）、OpenAI Responses（`/v1/responses`）、Google Gemini（`/v1beta/models/*:generateContent`）四种协议入口，各协议原生客户端可直接调用，均支持流式与非流式。
- **流式转发**：完整支持流式输出链路，工具调用与思考内容在流式场景下可正确传递。流式请求等待上游响应头最多 120 秒，收到响应头后连续 120 秒无数据才超时（心跳与部分分块也会刷新计时），持续输出的长回答不受固定总时限截断；非流式请求仍有 120 秒总时限，客户端取消与聚合主备配置的单成员时限继续生效。
- **供应商能力差异兼容**：通过可配置的能力描述层处理思考开关、`reasoning_effort`、流式用量等差异字段，新增供应商无需改动核心逻辑；能力规则还可细化到单个模型（模型级覆盖），如 DeepSeek V4 的 `reasoning_content` 保留回传与「缺失时关闭思考模式继续请求」降级策略。
- **从上游拉取模型**：添加模型时可从上游 `/models` 接口拉取可用列表，勾选批量添加。
- **Token 用量统计**：按时间区间、供应商、模型、应用维度统计 Token 用量，区分输入、输出与缓存命中；上游未返回用量时本地估算并标记来源。
- **调用日志**：记录每次调用的输入输出、耗时、状态码、协议、是否流式等信息，支持分页查看与导出 CSV。
- **统计图表**：基于 ECharts 提供饼图、柱形图、折线图，支持按小时、按天、按周统计。
- **网关访问密钥**：生成与管理访问网关的密钥（仅用于统计口径，不拦截请求），支持命名、备注、复制与显示切换，删除为软删除。
- **网关端口可自定义**：在「设置」页面修改监听端口，保存后自动重启；支持局域网访问。
- **网关服务开关**：可在「设置」页面或系统托盘随时暂停/恢复网关服务而无需关闭应用；暂停时优雅等待在途请求结束，端口设置仍可编辑并在下次开启时生效。
- **系统托盘**：应用运行时常驻系统托盘图标，双击或右键即可显示主窗口；右键菜单支持暂停/恢复网关服务与退出应用，并与设置页开关双向同步。
- **最小化与关闭隐藏到托盘**：可选最小化/关闭窗口时隐藏到托盘而非退出（默认开启），支持启动时自动隐藏到托盘，首次隐藏时弹出气泡提示。
- **开机启动**：可选开机自启（通过系统注册表实现，无需管理员权限）。
- **本机访问地址**：设置页列出本机回环、各网卡 IPv4 与主机名形式的访问地址，方便局域网设备接入，每条地址支持一键复制。
- **应用内自动更新**：检查 GitHub Release 最新稳定版，下载并校验 NSIS 安装包后静默覆盖安装并重启；安装时应用会明确退出（不受「关闭时隐藏到系统托盘」影响），安装程序启动失败则保持应用继续运行。

## 环境要求

- **Go** 1.25.0+（见 `go.mod`）
- **Node.js** 22+（前端构建）
- **Wails CLI** v2.13.0
- **NSIS**：仅打包安装包时需要（`build.ps1` 调用 `wails build -nsis`，依赖 `makensis`；CI 通过 `choco install nsis` 安装）
- **Windows**：当前发布 Windows x64 安装包；系统托盘、开机启动等桌面集成功能仅在 Windows 上完整可用，macOS/Linux 上为不崩溃的兼容占位（开发也可在 macOS/Linux 上运行 `wails dev`）

## 快速开始

```powershell
# 安装 Wails CLI
go install github.com/wailsapp/wails/v2/cmd/wails@v2.13.0

# 安装前端依赖
cd frontend; npm install; cd ..

# 开发模式（热重载）
wails dev

# 构建单文件可执行程序
wails build

# 构建 Windows NSIS 安装包（用户级 + 机器级）
.\build.ps1 -InstallScope both
```

开发模式下应用窗口启动后，本地网关默认监听 `http://127.0.0.1:8718`（端口可在「设置」页面修改）。调用示例：

```bash
curl http://127.0.0.1:8718/v1/chat/completions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer <你的网关密钥>" \
  -d '{
    "model": "<供应商ID>/<模型ID>",
    "messages": [{"role": "user", "content": "你好"}]
  }'
```

## 测试

```powershell
go test ./...
```

## 项目结构

```
main.go              Wails 应用入口
app.go               前端绑定（供应商/模型/密钥/统计/网关/更新等）
app_desktop.go       桌面设置前端绑定（托盘行为、开机启动等）
desktop_windows.go   Windows 平台系统托盘、开机启动、窗口状态监听
desktop_other.go     非 Windows 平台的桌面集成占位（不崩溃兼容）
updater.go           应用内自动更新
internal/
  ir/                内部统一协议格式（非流式 Request/Response + 流式 StreamEvent）
  capabilities/      供应商能力描述层（协议、思考、reasoning_effort、流式用量、模型级覆盖等）
  protocol/
    openaichat/      OpenAI Chat 入站解析与出站转换
    anthropic/       Anthropic Messages 出站转换
    gemini/          Google Gemini 出站转换
    openairesponses/ OpenAI Responses 出站转换
  relay/             HTTP 网关服务、流式转发、请求头透传、Token 估算
  store/             SQLite 存储层、迁移、预设、统计查询、桌面设置
frontend/            React + TailwindCSS 前端
build/               Wails 打包配置（Windows NSIS、macOS plist）
docs/                设计文档（ir.md）
.github/workflows/   GitHub Actions 发布流水线
```

## 聚合路由说明

聚合模型的成员必须是已存在的真实模型，不能嵌套聚合模型。成员所属平台被停用时，该成员会被视为不可用而跳过（不进入冷却期），聚合请求改由其可用成员服务，全部成员都不可用时返回「聚合没有可用成员」。主备策略使用内存冷却期跳过刚失败的成员（默认 60 秒），并默认限制每次成员尝试为 10 秒；应用重启后这两类运行态会清零。为了容灾，主备切换采用 at-least-once 语义：如果上游已接收请求但连接在响应前失败，备成员可能收到同一请求一次。成功调用的 `model` 会返回实际成员的 `providerId/modelId`，日志和 Token 统计也归属实际成员，同时保留聚合来源以便筛选追踪。

## 技术栈

- **桌面壳与后端**：Go + Wails v2
- **前端**：React 19 + TailwindCSS 4 + ECharts 6
- **存储**：SQLite（纯 Go 驱动 `modernc.org/sqlite`，无 CGO 依赖）
- **打包**：Wails NSIS（Windows x64 安装包）
- **CI/CD**：GitHub Actions，推送 `v*.*.*` / `V*.*.*` tag 触发构建并发布到 GitHub Releases

## 数据存储

配置与日志保存在用户配置目录下的 SQLite 数据库：`<UserConfigDir>/LocalRelay/localrelay.db`。API Key 等敏感字段以 AES 加密存储，绑定本机。
