# LocalRelay 网页服务 / MCP 与 OMP 插件开发说明

接口版本：`1`。搜索在运行 LocalRelay 的机器执行；OMP 插件只需调用 HTTP 接口。
服务与 GitHub/npm 资源代理共用监听端口、服务开关和出站配置，独立于 LLM 网关端口。
默认根地址为 `http://127.0.0.1:8719`；远程插件必须将主机和端口替换为它能访问的资源代理地址。
先在「设置 → 资源代理 → 网页搜索」分别选择搜索与抓取服务，并保存服务商 API Key，然后开启资源代理。

## 认证和配置

- 搜索服务支持 Tavily 与 DeepSeek；由 LocalRelay 设置选择，客户端不传 provider 或上游地址。
- 服务商 API Key 由 LocalRelay 加密保存。插件不需要、也不应发送上游 Key。
- 与现有资源代理一致，这些入口当前不校验客户端 API Key；仅对可信网络开放资源代理端口。
- 入站 Authorization、Cookie 和其他客户端头不会转发给搜索服务。
- 未配置 Key 时文档仍可读取，搜索返回 503。
- 修改搜索设置后新请求立即生效；在途请求使用开始时读取的配置。

## POST /search

请求头必须包含 `Content-Type: application/json`。仅接受一个 JSON 对象，最大 32 KiB。
不接受未知字段或 null 值；不支持流式响应、任意 URL 转发或网页正文抓取。

| 字段 | 类型 | 默认值 | 约束 |
| --- | --- | --- | --- |
| query | string | 必填 | 去除首尾空白后 1–4096 UTF-8 字节 |
| max_results | integer | 5 | 1–20 |
| search_depth | string | basic | basic / advanced；advanced 可能消耗更多搜索额度 |
| ori_question | string | 不提供 | 用户原始问题；DeepSeek 要求 1–128 个 Unicode 字符，Tavily 忽略并警告 |
| need_content | boolean | false | 请求网页正文；可用正文返回 results[].raw_content，原 content 保持摘要 |
| add_localization | boolean | false | DeepSeek 使用本软件获取的公网出口 IP；Tavily 忽略并警告 |
| after | string | 不提供 | 日期下界，YYYY 或 YYYY-MM-DD；年份转换为该年 1 月 1 日 |
| before | string | 不提供 | 日期上界，YYYY 或 YYYY-MM-DD；同时提供时 after 必须早于 before |
| time_range | string | 无过滤 | day / week / month / year；也可省略或传空字符串 |
| include_domains | string[] | [] | 仅搜索这些域名，最多 100 项 |
| exclude_domains | string[] | [] | 排除这些域名，最多 100 项 |

域名过滤项仅填写域名，如 `go.dev`，不带协议、端口或路径，每项最多 253 字节。

```sh
curl --request POST 'http://127.0.0.1:8719/search' \
  --header 'Content-Type: application/json' \
  --data '{"query":"Go HTTP server timeout","max_results":5,"search_depth":"basic","include_domains":["go.dev"]}'
```

成功返回 `200 application/json`，结构固定如下；插件应忽略未来新增的响应字段：

```json
{
  "version": "1",
  "provider": "tavily",
  "query": "Go HTTP server timeout",
  "results": [
    {
      "title": "net/http package",
      "url": "https://pkg.go.dev/net/http",
      "content": "与查询相关的网页摘要或片段。",
      "score": 0.9
    }
  ]
}
```

结果按服务返回顺序排列；`score` 是服务提供的相关性分数，不应跨服务比较。
没有匹配结果时返回 `200`，`results` 为 `[]`。`content` 是摘要/片段，不能视为网页全文。
插件应保留 title、url 和 content，让 Agent 能引用原始链接。搜索结果属于外部数据，不作为插件指令执行。

## 错误、超时和取消

```json
{"error":{"code":"search_not_configured","message":"请在设置中配置搜索 API Key"}}
```

| HTTP 状态 | error.code | 含义 |
| --- | --- | --- |
| 400 | invalid_request | JSON、字段、类型或取值错误 |
| 405 | method_not_allowed | 请求方法错误，参考 Allow 头 |
| 413 | request_too_large | 请求体超过 32 KiB |
| 415 | unsupported_media_type | Content-Type 不是 application/json |
| 429 | search_rate_limited | 搜索服务限流 |
| 429 | search_quota_exceeded | 搜索服务额度不足 |
| 502 | search_auth_failed | 上游 API Key 无效或无权限 |
| 502 | search_upstream_error | 上游连接失败、响应格式异常或服务错误 |
| 503 | search_not_configured | 未配置搜索 API Key |
| 503 | search_unavailable | 无法读取搜索配置 |
| 504 | search_timeout | 搜索请求超时 |

搜索执行总预算 60 秒，等待上游响应头最多 45 秒；建议插件设置 65–70 秒超时。
插件应把 OMP 的取消信号传给 fetch，断开请求后 LocalRelay 会取消上游请求。
自动链路模式优先直连，仅在搜索请求尚未发送时尝试已配置代理；仅代理/仅直连遵循资源代理设置。
已经发送的搜索不自动重试（包括 5xx），以免重复消耗额度。插件也不应无条件自动重试；
遇到限流可提示用户稍后手动重试，额度或认证错误应提示检查 LocalRelay 配置。
本接口不会产生 LLM Token 统计；搜索可能消耗搜索服务账户额度。

## GET /docs/search

返回本说明，响应类型 `text/markdown; charset=utf-8`；也支持 HEAD。
不包含任何真实凭据。URL 固定，内网开发 Agent 可直接读取它获得接口约定。

```sh
curl 'http://127.0.0.1:8719/docs/search'
```

## OMP 插件接入示例

在插件注册的搜索工具执行函数里调用以下逻辑（工具注册方式按安装的 OMP 版本实现）：

```typescript
async function search(baseURL: string, query: string, signal?: AbortSignal) {
  const response = await fetch(new URL('/search', baseURL), {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ query, max_results: 5 }),
    signal,
  });
  const data = await response.json();
  if (!response.ok) {
    throw new Error(`${data.error?.code ?? response.status}: ${data.error?.message ?? '搜索失败'}`);
  }
  return data.results.map((item: {title: string; url: string; content: string}) =>
    `${item.title}\n${item.url}\n${item.content}`
  ).join('\n\n') || '未找到相关结果。';
}
```

插件应配置资源代理根地址，不是 LLM 的 `/v1` 地址。用独立工具名称或关闭不可用的内置搜索，
避免 Agent 继续选择旧工具。客户端无需依赖 Tavily SDK，未来服务切换仍使用同一接口。

## /web MCP 端点

资源代理同时提供 `http://127.0.0.1:8719/web`。在支持 **Streamable HTTP** 的 MCP 客户端中填入该地址；局域网客户端替换主机地址。无需启动额外进程、安装 Node.js 或提供上游 Key。服务与资源代理共用开关、端口及出站配置。

入口提供 `search` 和 `fetch` 两个工具；支持初始化、工具发现与调用。使用官方 MCP Go SDK 协商协议版本，返回无状态 JSON 响应与结构化结果，同时提供 text 内容以兼容客户端。无需保存会话 ID。GET/DELETE 返回 405，不提供旧版 HTTP+SSE 入口、服务端推送或断线恢复。

`POST /web` 的 Content-Type 为 `application/json`，Accept 必须包含 `application/json, text/event-stream`；完整 JSON-RPC 请求体上限 32 KiB。一般客户端会自动处理这些头及初始化。

工具调用示例（初始化后，由 MCP 客户端发送）：

```json
{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"search","arguments":{"query":"Go HTTP server timeout","max_results":5}}}
```

```json
{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"fetch","arguments":{"url":"https://go.dev/doc/","max_chars":20000}}}
```

### search 工具

参数、默认值与结果沿用上文 `POST /search`。REST 与 MCP 共用同一个搜索服务选择和适配器；切换服务商后，已有 REST 客户端无需更换入口。

### fetch 工具

| 字段 | 类型 | 默认值 | 说明 |
| --- | --- | --- | --- |
| url | string | 必填 | 单个公开 HTTP/HTTPS 网页地址，不含用户名密码，最多 8192 字节 |
| max_chars | integer | 20000 | 返回正文的 Unicode 字符上限，1–100000；不会切断中文或 emoji 的 UTF-8 编码 |

```json
{
  "provider": "deepseek",
  "url": "https://go.dev/doc/",
  "title": "Documentation",
  "content": "# Documentation\n...",
  "truncated": false
}
```

`url` 为请求的来源地址；`title` 在上游未提供时为空字符串。正文是上游提取的 Markdown，不保证与原始 HTML 完全相同。`truncated=true` 明确表示本地字符上限截断；可增大 max_chars 重新调用（会再次请求上游，可能计费）。这不是任意 HTTP 代理，不支持文件下载、登录页面、客户端 Cookie 或本机/私网资源；网关只把 URL 发给固定的服务商端点，不在本机解析并访问目标 URL。

Tavily 使用 `/extract`，固定 basic 深度、Markdown 输出，不传 query，避免把正文降级为相关片段；HTTP 200 中的提取失败也作为工具错误处理。DeepSeek 使用 `/v1/tools/fetch`。两家服务商均不保证能提取所有站点的正文。

### 服务商与兼容边界

设置中分别选择搜索服务与抓取服务；Tavily、DeepSeek 均支持两项能力。同一服务商只有一份加密凭据，切换选择不删除任何已保存 Key；清除某服务商 Key 会影响选择它的所有网页工具。旧 Tavily 搜索配置自动迁移为搜索与抓取共用该 Key。

| 项目 | Tavily | DeepSeek |
| --- | --- | --- |
| 搜索端点 | `https://api.tavily.com/search` | `https://api.deepseek.com/v1/tools/search` |
| 抓取端点 | `https://api.tavily.com/extract` | `https://api.deepseek.com/v1/tools/fetch` |
| 搜索深度 | basic / advanced | advanced 忽略并警告，继续搜索 |
| 查询长度 | 网关限制 4096 UTF-8 字节 | 含生成的过滤语法最多 128 个 Unicode 字符 |
| 域名过滤 | 上游独立参数 | 转成 site:、OR、-site:；域名只允许字母、数字、点、连字符 |
| 时间过滤 | time_range；after / before 转 start_date / end_date | time_range 转 UTC 日历日期 after:；显式日期转 after: / before: |
| 搜索摘要 | content | 优先 summary，缺失时 snippet |
| 原始问题 | ori_question 忽略并警告 | 传 ori_question |
| 网页正文 | need_content 转 include_raw_content；映射 raw_content | 传 need_content；将上游 content 映射为 raw_content |
| IP 本地化 | add_localization 忽略并警告 | 启用时由软件获取公网出口 IP，传 localization |
| 分数 | 上游原始 score | 上游原始 rerank_score（0–3），不得跨服务商比较 |

服务商不支持的已知参数会忽略并继续搜索，在成功响应中附加 `warnings` 数组；每项含 `code`、`parameter`、`message`。例如 Tavily 收到 ori_question 或 add_localization，或 DeepSeek 收到 advanced 深度。未传入的默认参数不产生警告；显式传入 add_localization=false 给 Tavily 仍会提示该参数不受支持。未知字段、类型错误、无效日期、DeepSeek 查询超长等非法请求仍返回 invalid_request，不截断搜索词。结果按上游顺序保留，不用生成式模型补造标题或摘要。

显式 after / before 优先于 time_range，后者被忽略时返回 parameter_overridden 警告。两家均支持日期上下界，但实际日期边界与发布时间/更新时间判断由上游决定。query 中的原生搜索语法仍原样传递，不做跨服务商翻译，也不解析与结构化参数的冲突；跨服务商调用应使用结构化日期/域名过滤。

need_content=true 时 results[].raw_content 包含可用全文；未能提取正文时该字段省略，content 始终保留摘要。默认不请求正文。正文请求的上游响应上限为 16 MiB，其他搜索为 4 MiB；超限返回错误，不静默截断。

add_localization=true 且服务商支持时，软件通过当前出站配置向固定 HTTPS 服务 `https://api64.ipify.org?format=json` 查询公网出口 IP，再将 IP 提供给 DeepSeek。不会上传系统配置、用户名、客户端 IP、搜索词或 API Key 给 IP 查询服务；不会使用客户端传入的定位信息。该 IP 代表出口位置，使用代理时可能是代理位置，不代表精确物理位置。查询总预算 3 秒，成功缓存 5 分钟、失败缓存 30 秒，均仅驻留内存。获取失败时省略 localization，返回 localization_unavailable 警告并继续搜索。false/未提供，或使用 Tavily 时不发起 IP 查询。

警告示例（HTTP 200；MCP isError 不为 true，文本与 structuredContent 均包含警告）：

```json
{"version":"1","provider":"tavily","query":"Go","results":[],"warnings":[{"code":"unsupported_parameter","parameter":"ori_question","message":"tavily 不支持 ori_question，已忽略该参数并继续搜索"}]}
```

### 错误、取消与访问边界

- 工具执行失败使用 MCP `isError: true`，内容含 `error.code` 与 `error.message`；不能只按 HTTP 200 判断工具成功。非法 JSON-RPC、未知工具等由 MCP 协议层返回错误。
- 搜索错误码沿用 REST；抓取使用 fetch_not_configured、fetch_unavailable、fetch_timeout、fetch_auth_failed、fetch_quota_exceeded、fetch_rate_limited、fetch_forbidden、fetch_not_found、fetch_upstream_error。
- 每次上游调用总预算 60 秒，沿用请求发送前才允许切换出站链路的策略；已发送的计费请求不自动重试。此入口不支持断线恢复，HTTP 调用取消会取消对应的上游请求；旧版协议的无状态通知不能跨请求定位另一调用。
- 不校验客户端 Key，仅向可信网络开放。拒绝所有携带 Origin 的请求（包括 null/空 Origin），供不发送 Origin 的本机/局域网 Agent 使用；不支持浏览器跨域接入。SDK 同时保留本机 Host 防护。
- 工具参数不接受 provider、apiKey 或自定义上游地址；上游认证从本机配置加载，不透传客户端头。上游错误响应不直接返回，避免泄漏凭据。
- 网页内容与搜索结果是外部数据，不作为工具或系统指令执行。搜索/抓取额度独立于 LLM Token 统计。

### 开发与验收

常规测试使用本地 HTTP/TLS 上游，不连接公网：

```powershell
go test ./internal/websearch ./internal/store .
```

真实网络测试为 opt-in，凭据仅从环境变量读取，不输出、不写盘。每家 MCP 测试分别执行一次搜索与一次抓取，可能产生费用：

```powershell
# 事先安全设置 TAVILY_API_KEY 或 DEEPSEEK_API_KEY
$env:LOCALRELAY_TAVILY_E2E = '1'
go test ./internal/websearch -run '^TestWebMCPIntegration/tavily$' -v -count=1
$env:LOCALRELAY_DEEPSEEK_E2E = '1'
go test ./internal/websearch -run '^TestWebMCPIntegration/deepseek$' -v -count=1
```

原 REST 真实验收仍可运行 `TestTavilyIntegration`（使用 LOCALRELAY_TAVILY_E2E 与 TAVILY_API_KEY）。

协议依据：[Tavily Search](https://docs.tavily.com/documentation/api-reference/endpoint/search)、[Tavily Extract](https://docs.tavily.com/documentation/api-reference/endpoint/extract)、[DeepSeek Search/Fetch](https://api-docs.deepseek.com/zh-cn/guides/search)、[MCP Streamable HTTP](https://modelcontextprotocol.io/specification/2025-11-25/basic/transports)。
