# 资源代理（GitHub + npm + 网页搜索）

在「设置 → 资源代理」配置监听端口和出站链路，独立点击「保存资源代理设置」；然后到「资源代理」页面开启服务。默认端口为 `8719`，默认关闭，开关状态随应用重启保留。GitHub 与 npm 共用端口、服务开关和出站配置；该服务与 LLM 网关的设置、启停、调用日志及 Token 统计相互独立。升级保留原 GitHub 代理配置，无须重新设置。

服务监听本机所有 IPv4 网卡，页面提供回环、网卡与主机名地址及复制按钮。局域网设备使用宿主机的局域网 IP，并确保防火墙允许该端口。关闭后不再监听；再次开启即可接收请求。运行中修改端口会最多等待在途请求 3 秒，再结束剩余连接；新端口无法绑定时保留原服务和配置。仅修改出站配置时，新请求立即采用新配置，在途请求继续完成。

## GitHub 地址与用法

将 `https://github.com` 替换为 `http://127.0.0.1:8719/github`（远程客户端需替换主机地址）：

```sh
git clone http://127.0.0.1:8719/github/octocat/Hello-World.git
curl -o source.zip http://127.0.0.1:8719/github/octocat/Hello-World/archive/refs/heads/master.zip
curl -o asset.zip http://127.0.0.1:8719/github/OWNER/REPO/releases/download/TAG/asset.zip
curl http://127.0.0.1:8719/github/octocat/Hello-World/raw/master/README
```

也支持 `/releases/latest/download/文件名`。下载保留 Range、If-Range、ETag、Content-Range 与二进制响应，GitHub 的源码归档和 Release 重定向由服务端跟随，客户端无须直连重定向后的站点。

已有仓库可以使用 Git 的 `insteadOf`，只修改当前仓库的本地配置：

```sh
git config --local url."http://127.0.0.1:8719/github/".insteadOf "https://github.com/"
# 撤销
git config --local --remove-section 'url.http://127.0.0.1:8719/github/'
```

首次克隆直接使用代理地址即可。该规则也会匹配 HTTPS 推送地址；通过此服务推送会被拒绝，写入请使用独立的直连 push URL。

## npm 地址与用法

将 npm 官方 registry 替换为 `http://127.0.0.1:8719/npm/`，局域网客户端请使用运行 LocalRelay 的机器地址：

```sh
npm install lodash --registry=http://127.0.0.1:8719/npm/
npm install @types/node --registry=http://127.0.0.1:8719/npm/
npm ci --registry=http://127.0.0.1:8719/npm/
npm ping --registry=http://127.0.0.1:8719/npm/
npm search lodash --registry=http://127.0.0.1:8719/npm/
npm audit --registry=http://127.0.0.1:8719/npm/
```

也可在项目 `.npmrc` 中添加 `registry=http://127.0.0.1:8719/npm/`，删除该行即可恢复默认。已有 `@scope:registry` 配置需单独调整，它会优先于默认 registry。

支持普通包、作用域包（含 `@scope%2Fname` 编码）、版本/标签查询、`.tgz` 下载、搜索、ping、签名公钥读取，以及 Bulk Advisory / Quick Audit 两种审计 POST。其余写入方法与登录接口被拒绝；仅支持公开包，入站认证、Cookie 和自定义凭据不透传。

元数据中的官方 `dist.tarball` 地址改写为当前客户端访问的本地 `/npm/` 地址，未知字段、integrity 与 signatures 保留。元数据限制为解压后 64 MiB，支持 identity/gzip；改写后重算 Content-Length，移除上游 ETag、Last-Modified 与范围头，使用 `Cache-Control: no-store` 避免缓存旧端口或其他机器地址。压缩包保持原字节流，支持 Range 与缓存验证。

npm 的下载域名替换在部分版本会丢弃 registry 的路径前缀，因此服务还接受受限的根路径 `/{package}/-/*.tgz`（包括作用域包与 `npm` 包本身），专用于官方源旧锁文件兼容。该入口仅允许 GET/HEAD，不是通用根路径代理。官方源旧锁文件依赖 npm 默认的 `replace-registry-host=npmjs`，设置为 `never` 会绕过此兼容机制。

目标及重定向仅允许 HTTPS `registry.npmjs.org`（443），最多 10 跳，重定向仍检查允许的接口与方法。只有搜索参数和 ping 的 `write` 参数可透传；其他查询参数被拒绝。包元数据中的第三方下载地址保持原样；其他镜像/旧本地端口写入的锁文件、安装脚本、Git 依赖和外部二进制下载不保证经过本入口。暂不承诺 pnpm/Yarn 兼容，不提供磁盘缓存、私有包认证或发布功能。

协议依据：[npm registry API](https://github.com/npm/registry/blob/main/docs/REGISTRY-API.md)、[npm 包元数据](https://github.com/npm/registry/blob/main/docs/responses/package-metadata.md)、[npm audit](https://docs.npmjs.com/cli/v11/commands/npm-audit/)、[pacote 下载地址替换](https://github.com/npm/pacote/blob/main/lib/remote.js)。

## 出站链路

以下重试和缓存说明针对 GitHub/npm；网页搜索共用链路配置，但使用后文描述的独立重试策略。

- **自动选择**：优先直连，连接错误、超时或上游 5xx 时尝试另一条链路；未配置代理时只有直连。
- **仅直连**：直接访问对应上游，不读取系统环境变量中的 HTTP 代理。
- **仅代理**：强制使用填写的代理，不回退到直连。

代理类型支持 HTTP CONNECT 和 SOCKS5（代理端解析目标域名）。地址格式为 `127.0.0.1:7890` 或 `[::1]:1080`，不含协议前缀。目前仅支持无需认证的代理，拒绝带用户名、密码的地址。

实际请求作为可达性探测：成功链路按仓库或 npm 包缓存 5 分钟（clone 的 `.git` 地址与下载地址共享仓库键），失败链路缓存 30 秒，各最多缓存 1024 个键。GitHub 和 npm 的探测缓存相互独立，避免其中一个站点失败影响另一个站点。失败缓存过期后，新仓库/包可重新尝试直连。切换只发生在下游尚未收到响应时；已经开始的下载不会拼接不同链路的数据，遇到截断会关闭连接，让客户端检测到失败并重试。

TCP 连接与 TLS 握手各限 5 秒，单次响应头等待限 8 秒，一次链路尝试的代理握手与所有重定向合计限 12 秒。响应内容按块转发，连续 2 分钟未读到数据才取消，不设置下载总时限。

## GitHub 支持边界

仅支持公开仓库的只读 Smart HTTP 克隆/拉取、源码归档、Release 文件和 raw 文件。保留 Git-Protocol 请求头以支持协议 v2，拒绝 `git-receive-pack`、推送及其他写入方法。不支持私有仓库认证、Git LFS、GitHub API 或通用网页代理。

仅允许 HTTPS 目标 `github.com`、`codeload.github.com`、`raw.githubusercontent.com`、`objects.githubusercontent.com`、`release-assets.githubusercontent.com`；重定向逐跳验证精确主机、端口及协议，最多 10 跳。入站 Authorization、Cookie、API Key、自定义凭据头不会出站，下载查询参数不透传。GitHub 自身产生的签名重定向参数会保留以下载 Release 资产。

协议依据：[Git Smart HTTP](https://git-scm.com/docs/http-protocol)、[GitHub Release 资产](https://docs.github.com/en/rest/releases/assets)、[GitHub 仓库内容](https://docs.github.com/en/rest/repos/contents)。Release 资产主机已通过真实下载验证。

## 网页搜索与 OMP 插件

在「设置 → 资源代理 → 网页搜索」选择服务（目前只有 Tavily），填写 API Key，点击「保存搜索设置」。Key 使用本机存储的 AES-GCM 加密保存，设置页只返回是否已配置；编辑时留空保留原 Key，勾选清除后保存才会删除。搜索设置独立保存，不改变资源代理开关、端口和出站配置，保存后新请求立即生效。

开启资源代理后，同一端口新增两个精确路径：

| 接口 | 用途 |
| --- | --- |
| `POST /search` | 统一搜索接口，返回 JSON 格式的标题、原始链接、摘要和相关性分数 |
| `GET /docs/search` | 返回完整 Markdown 接口说明，供内网开发 Agent 编写 OMP 插件；支持 HEAD |

默认根地址为 `http://127.0.0.1:8719`。资源代理页面可复制不同网卡的搜索和文档地址；内网机器需要能够访问该资源代理端口，LLM 网关可达并不代表此端口已放行。

```sh
curl http://127.0.0.1:8719/docs/search
curl --request POST http://127.0.0.1:8719/search --header 'Content-Type: application/json' --data '{"query":"Go HTTP server timeout","max_results":5}'
```

完整且随程序发布的接口文档源文件为 [search.md](../internal/websearch/search.md)，由服务直接内嵌返回。文档包括字段限制、默认值、成功/空结果/错误响应、取消与重试说明，以及 TypeScript fetch 示例。OMP 插件在内网独立开发，不依赖 Tavily SDK，也不接收上游 Key；未来更换搜索服务仍可使用同一接口。

搜索请求只发送到所选服务的固定端点（Tavily 为 `https://api.tavily.com/search`），不接受自定义目标 URL，不跟随重定向，不转发入站凭据或 Cookie。不提供网页全文抓取或通用 HTTP/CONNECT 代理。与现有资源代理一致，两个接口不校验客户端凭据，服务应只向可信网络开放；搜索消耗搜索服务额度，不计入 LLM Token 统计。

搜索共用资源代理链路模式：仅直连不读取系统代理，仅代理使用已配置的 HTTP/SOCKS5 代理，自动模式优先直连且仅在请求尚未发送时切换代理。已经发送的搜索不自动重试，即使返回 5xx，以避免重复计费。搜索不使用 GitHub/npm 的链路缓存；总执行预算 60 秒，响应头等待最多 45 秒，客户端取消会传播到上游。请求体限制 32 KiB，上游结果限制 4 MiB。

上游协议依据：[Tavily Search API](https://docs.tavily.com/documentation/api-reference/endpoint/search)。初版支持 basic/advanced 深度、结果数量、时间范围和域名过滤，关闭上游自动参数、答案生成和全文返回，以保持稳定的插件接口和明确的搜索成本。

## 验证

常规检查：

```powershell
go vet ./...
go test ./...
cd frontend
npm run build
```

可选真实网络验收（需要能够从开发机访问 GitHub）：

```powershell
$env:LOCALRELAY_GITHUB_E2E = '1'
go test ./internal/githubproxy -run TestGitHubIntegration -v -timeout 10m
Remove-Item Env:LOCALRELAY_GITHUB_E2E
```

验收使用临时目录和本地 HTTP CONNECT/SOCKS5 代理，不修改用户 Git 配置。四种场景分别验证直连、HTTP、SOCKS5、模拟直连故障后的自动回退：对 `git/git` 执行浅克隆与 `git fsck --full`，比较工作树版本，下载 shfmt Release 并比较 SHA-256，同时验证 archive 和 raw 下载。浅克隆包含该版本完整工作树，不下载仓库全部历史。

npm 真实验收（需安装 npm 并能访问官方 registry）：

```powershell
$env:LOCALRELAY_NPM_E2E = '1'
go test ./internal/githubproxy -run TestNPMIntegration -v -timeout 8m
Remove-Item Env:LOCALRELAY_NPM_E2E
```

四种链路均在临时项目与独立 npm 配置/缓存中安装普通包和作用域包（禁用安装脚本），验证 ping、真实审计报告，再将锁文件还原为官方源地址，以空缓存执行 `npm ci`。服务记录请求路径以确认元数据、压缩包、旧锁文件下载与审计实际经过本地入口；npm 校验锁文件 integrity，确保包内容完整。

搜索的常规测试通过本地 HTTP/TLS 测试服务器验证请求映射、凭据隔离、错误映射、配置即时生效、取消和避免重复计费的重试边界，不访问公网。真实 Tavily 验收显式开启后才运行，消耗一次 basic 搜索额度；先在本机安全设置 `TAVILY_API_KEY` 环境变量，再执行：

```powershell
$env:LOCALRELAY_TAVILY_E2E = '1'
go test ./internal/websearch -run TestTavilyIntegration -v -count=1
Remove-Item Env:LOCALRELAY_TAVILY_E2E
```
