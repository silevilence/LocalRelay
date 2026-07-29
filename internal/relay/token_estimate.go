package relay

import (
	"math"
	"unicode"

	"localrelay/internal/ir"
)

// estimateResponseUsage provides a clearly marked local approximation only
// when an upstream success response omitted usage entirely. Provider-reported
// usage always takes precedence over this fallback.
func estimateResponseUsage(request ir.Request, response ir.Response) ir.Usage {
	usage := estimateRequestUsage(request)
	for _, choice := range response.Choices {
		usage.OutputTokens += estimateMessage(choice.Message)
	}
	return usage
}

func estimateStreamUsage(request ir.Request, output string) ir.Usage {
	usage := estimateRequestUsage(request)
	usage.OutputTokens = estimateText(output)
	return usage
}

func estimateRequestUsage(request ir.Request) ir.Usage {
	usage := ir.Usage{}
	for _, message := range request.Messages {
		usage.InputTokens += estimateMessage(message)
	}
	for _, tool := range request.Tools {
		usage.InputTokens += estimateText(tool.Name + tool.Description + string(tool.Parameters))
	}
	return usage
}

func estimateMessage(message ir.Message) int {
	tokens := 0
	for _, block := range message.Content {
		switch block.Type {
		case ir.BlockImage:
			// Vision token costs are provider-specific. Keep a fixed estimate and
			// expose the entire call as estimated in the statistics UI.
			tokens += 256
		case ir.BlockToolCall:
			tokens += estimateText(block.ToolName + string(block.Arguments))
		case ir.BlockToolResult:
			tokens += estimateText(block.Result)
		default:
			tokens += estimateText(block.Text)
		}
	}
	return tokens
}

func estimateText(text string) int {
	if text == "" {
		return 0
	}
	var ascii, other int
	for _, r := range text {
		if unicode.IsSpace(r) || r <= unicode.MaxASCII {
			ascii++
		} else {
			other++
		}
	}
	return int(math.Ceil(float64(ascii)/4)) + other
}
