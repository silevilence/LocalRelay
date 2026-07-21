# 内部统一协议格式（IR，非流式）

LocalRelay 的非流式 IR 以消息数组为中心：外部协议先转成 `ir.Request`，再从 `ir.Request` 转到上游供应商请求。

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

`thinking` block 在 OpenAI-compatible 出站请求中没有通用字段，会被有意丢弃；未来供应商能力配置落地后再接入有私有字段支持的供应商。

OpenAI-compatible assistant 消息携带 `tool_calls` 时，`content` 只能是 `null` 或字符串；如果 IR 里同一条 assistant 消息混入图片块，出站映射会丢弃图片块并保留文本与工具调用，避免生成上游拒绝的请求。
