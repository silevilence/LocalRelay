# LocalRelay 搜索接口 / OMP 插件开发说明

接口版本：`1`。搜索在运行 LocalRelay 的机器执行；OMP 插件只需调用 HTTP 接口。
服务与 GitHub/npm 资源代理共用监听端口、服务开关和出站配置，独立于 LLM 网关端口。
默认根地址为 `http://127.0.0.1:8719`；远程插件必须将主机和端口替换为它能访问的资源代理地址。
先在「设置 → 资源代理 → 网页搜索」选择搜索服务并保存 API Key，然后开启资源代理。

## 认证和配置

- 当前搜索服务为 Tavily；由 LocalRelay 设置选择，客户端不传 provider 或上游地址。
- Tavily API Key 由 LocalRelay 加密保存。插件不需要、也不应发送 Tavily Key。
- 与现有资源代理一致，这两个入口当前不校验客户端 API Key；仅对可信网络开放资源代理端口。
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
