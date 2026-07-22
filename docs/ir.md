# 内部统一协议格式（IR）

LocalRelay 的 IR 以消息数组为中心：外部协议先转成 `ir.Request`，再从 `ir.Request` 转到上游供应商请求。流式响应不复用非流式 `Response`，而是使用独立事件序列。

## 核心结构

- `Request`：模型、消息、工具定义、通用采样参数。
- `Response`：非流式响应骨架，包含一个或多个 `Choice` 与 token `Usage`。
- `Message`：`system` / `user` / `assistant` / `tool` 四类角色。
- `ContentBlock`：用 `type` 区分文本、图片、工具调用、工具结果、思考内容。
- `Tool`：目前对应 OpenAI Chat 的 function tool。

Go struct 位于 `internal/ir`。

## 字段语义

- `ContentBlock.text`：`text` block 的正文；在 `thinking` block 中表示思考内容。
- `ContentBlock.imageUrl`：图片远程 URL 或 data URL；`detail` 保留 OpenAI Chat 的图片清晰度提示。
- `ContentBlock.toolCallId`：连接 assistant 的 `tool_call` 与后续 `tool_result`。
- `ContentBlock.arguments`：工具调用参数，保存为 JSON 原文。
- `ContentBlock.signature`：供应商可能返回的 thinking/reasoning 校验或签名元数据；没有则为空。
- `Usage.cacheCreationInputTokens` / `cacheReadInputTokens`：供应商支持缓存 token 时填写，不支持时保持 0。

## 手工示例

### 纯文本

```json
{
  "model": "openai/gpt-4.1-mini",
  "messages": [
    {"role": "system", "content": [{"type": "text", "text": "You are concise."}]},
    {"role": "user", "content": [{"type": "text", "text": "Hello"}]}
  ]
}
```

### 工具调用

```json
{
  "model": "openai/gpt-4.1-mini",
  "tools": [
    {
      "type": "function",
      "name": "get_weather",
      "parameters": {"type": "object", "properties": {"city": {"type": "string"}}}
    }
  ],
  "messages": [
    {"role": "user", "content": [{"type": "text", "text": "Weather in Shanghai?"}]},
    {
      "role": "assistant",
      "content": [
        {"type": "thinking", "text": "Need current weather."},
        {"type": "tool_call", "toolCallId": "call_1", "toolName": "get_weather", "arguments": {"city": "Shanghai"}}
      ]
    },
    {"role": "tool", "content": [{"type": "tool_result", "toolCallId": "call_1", "result": "{\"tempC\":31}"}]}
  ]
}
```

### 多模态

```json
{
  "model": "openai/gpt-4.1-mini",
  "messages": [
    {
      "role": "user",
      "content": [
        {"type": "text", "text": "What is in this image?"},
        {"type": "image", "imageUrl": "data:image/png;base64,iVBORw0KGgo=", "detail": "low"}
      ]
    }
  ]
}
```

当前已实现：

- OpenAI Chat 请求体 → IR。
- IR → OpenAI-compatible Chat 请求体，先支持 `openai` 和 `deepseek`。
- OpenAI Chat SSE chunk → 流式 IR 事件 → OpenAI Chat SSE chunk。

`thinking` block 在 OpenAI-compatible 出站请求中没有通用字段，会被有意丢弃；未来供应商能力配置落地后再接入有私有字段支持的供应商。

OpenAI-compatible assistant 消息携带 `tool_calls` 时，`content` 只能是 `null` 或字符串；如果 IR 里同一条 assistant 消息混入图片块，出站映射会丢弃图片块并保留文本与工具调用，避免生成上游拒绝的请求。

## 流式响应 IR

流式 IR 参考 Anthropic 的事件流语义，位于 `internal/ir/stream.go`。事件顺序约束如下：

1. `message_start`：一次响应开始，记录上游响应 id 与 model。
2. `choice_start`：某个 choice 的 assistant 消息开始，通常来自 OpenAI Chat 的 `delta.role`。
3. `content_block_start`：某个内容块开始，`blockType` 可为 `text`、`thinking` 或 `tool_call`。
4. `content_block_delta`：内容块增量；文本与思考内容使用 `delta`，工具调用参数使用 `argumentsDelta`。
5. `content_block_stop`：该 choice 已打开的内容块结束，通常由 `finish_reason` 触发。
6. `message_delta`：消息级增量，目前用于 `stopReason` 与最终 `usage`。
7. `message_stop`：收到 OpenAI Chat SSE 的 `[DONE]`。

流式和非流式路径刻意分离：非流式响应先完整解析为 `ir.Response`；流式响应逐个 SSE payload 转成 `ir.StreamEvent` 并立即写回客户端，避免缓存完整输出。

### 转回 OpenAI Chat SSE 的降级映射

| IR 事件 | OpenAI Chat SSE 表达 | 说明 |
|---------|----------------------|------|
| `message_start` | 不单独输出 | OpenAI Chat 没有消息开始事件，由首个 `delta.role` 隐含。 |
| `choice_start` | `choices[].delta.role` | 保留 choice index 与 assistant role。 |
| `content_block_start(text/thinking)` | 不单独输出 | OpenAI Chat 没有文本/思考块开始事件，由首个内容 delta 隐含。 |
| `content_block_start(tool_call)` | `choices[].delta.tool_calls[]` | 工具调用有显式 index/name/id，可保留。 |
| `content_block_delta(text)` | `choices[].delta.content` | 保留文本增量。 |
| `content_block_delta(thinking)` | `choices[].delta.reasoning_content` | 仅在供应商能力配置声明支持 `reasoning_content` 时输出；否则降级丢弃，避免泄漏到不支持该字段的 OpenAI 兼容客户端。 |
| `content_block_delta(tool_call)` | `choices[].delta.tool_calls[].function.arguments` | 保留工具参数增量。 |
| `content_block_stop` | 不单独输出 | OpenAI Chat 由 `finish_reason` 或 `[DONE]` 隐含块结束。 |
| `message_delta(stopReason)` | `choices[].finish_reason` | 保留停止原因。 |
| `message_delta(usage)` | `choices: []` + `usage` | usage-only 尾包仍必须带空 `choices`，兼容严格客户端校验。 |
| `message_stop` | `data: [DONE]` | 保留流结束标记。 |
| `error` | `data: {"error": ...}` | 上游 SSE 解析/读取失败时回传错误事件，随后结束流。 |

固定的 `thinking=0`、`text=1`、`tool=2+` block index 只用于让 IR 事件可引用稳定块；真实展示顺序以事件到达顺序为准，工具调用 index 保留 OpenAI Chat 原始 `tool_calls[].index`。
