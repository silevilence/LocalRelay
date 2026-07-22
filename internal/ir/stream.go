package ir

type StreamEventType string

const (
	StreamMessageStart      StreamEventType = "message_start"
	StreamChoiceStart       StreamEventType = "choice_start"
	StreamContentBlockStart StreamEventType = "content_block_start"
	StreamContentBlockDelta StreamEventType = "content_block_delta"
	StreamContentBlockStop  StreamEventType = "content_block_stop"
	StreamMessageDelta      StreamEventType = "message_delta"
	StreamMessageStop       StreamEventType = "message_stop"
	StreamError             StreamEventType = "error"
)

type StreamEvent struct {
	Type        StreamEventType `json:"type"`
	ID          string          `json:"id,omitempty"`
	Model       string          `json:"model,omitempty"`
	ChoiceIndex int             `json:"choiceIndex,omitempty"`
	BlockIndex  int             `json:"blockIndex,omitempty"`
	Role        Role            `json:"role,omitempty"`
	BlockType   BlockType       `json:"blockType,omitempty"`

	Delta          string `json:"delta,omitempty"`
	ToolCallID     string `json:"toolCallId,omitempty"`
	ToolName       string `json:"toolName,omitempty"`
	ArgumentsDelta string `json:"argumentsDelta,omitempty"`

	StopReason string `json:"stopReason,omitempty"`
	Usage      Usage  `json:"usage,omitempty"`
	Error      string `json:"error,omitempty"`
}
