package contextmgr

import (
	"encoding/json"
	"math"
	"strings"
	"unicode"
	"unicode/utf8"

	"elbot/internal/llm"
)

const (
	DefaultMaxPromptRatio        = 0.8
	DefaultSingleMessageMaxRatio = 0.5
	DefaultReserveOutputTokens   = 0
	DefaultUserOriginalMaxRunes  = 4000
	imageTokenEstimate           = 1024
	MessageTokenOverhead         = 4
)

// PromptBudget is the estimated input budget for one LLM request.
type PromptBudget struct {
	Window                int
	InputLimit            int
	SingleMessageLimit    int
	ReserveOutputTokens   int
	MaxPromptRatio        float64
	SingleMessageMaxRatio float64
}

// NewPromptBudget builds a budget. reserve <= 0 means calculate a conservative
// value from the window size.
func NewPromptBudget(window int, maxPromptRatio, singleMessageMaxRatio float64, reserve int) PromptBudget {
	if window <= 0 {
		return PromptBudget{MaxPromptRatio: maxPromptRatio, SingleMessageMaxRatio: singleMessageMaxRatio}
	}
	if maxPromptRatio <= 0 || maxPromptRatio >= 1 {
		maxPromptRatio = DefaultMaxPromptRatio
	}
	if singleMessageMaxRatio <= 0 || singleMessageMaxRatio >= 1 {
		singleMessageMaxRatio = DefaultSingleMessageMaxRatio
	}
	if reserve <= 0 {
		reserve = window / 5
		if reserve > 4096 {
			reserve = 4096
		}
		if reserve < 8 {
			reserve = 8
		}
	}
	if reserve >= window {
		reserve = window / 5
	}
	inputLimit := int(float64(window) * maxPromptRatio)
	if window-reserve < inputLimit {
		inputLimit = window - reserve
	}
	if inputLimit < 0 {
		inputLimit = 0
	}
	singleLimit := int(float64(window) * singleMessageMaxRatio)
	if singleLimit < 0 {
		singleLimit = 0
	}
	return PromptBudget{
		Window:                window,
		InputLimit:            inputLimit,
		SingleMessageLimit:    singleLimit,
		ReserveOutputTokens:   reserve,
		MaxPromptRatio:        maxPromptRatio,
		SingleMessageMaxRatio: singleMessageMaxRatio,
	}
}

// EstimateTextTokens returns a conservative token estimate without requiring a
// provider tokenizer. It overestimates CJK text slightly and underestimates
// long ASCII runs deliberately less than bytes/4 would.
func EstimateTextTokens(text string) int {
	if text == "" {
		return 0
	}
	total := 0.0
	ascii := 0
	for _, r := range text {
		switch {
		case r < utf8.RuneSelf:
			ascii++
		case unicode.Is(unicode.Han, r),
			unicode.Is(unicode.Hangul, r),
			unicode.Is(unicode.Hiragana, r),
			unicode.Is(unicode.Katakana, r),
			(r >= 0xFF00 && r <= 0xFFEF):
			total += 1.0
		default:
			total += 0.65
		}
	}
	total += float64(ascii) / 3.5
	if total < 1 {
		total = 1
	}
	return int(math.Ceil(total))
}

func EstimateMessagesTokens(messages []llm.LLMMessage) int {
	total := 0
	for _, message := range messages {
		total += EstimateMessageTokens(message)
	}
	return total
}

func EstimateMessageTokens(message llm.LLMMessage) int {
	total := MessageTokenOverhead
	for _, segment := range message.Segments {
		switch segment.Type {
		case llm.SegmentText:
			total += EstimateTextTokens(segment.Text)
		case llm.SegmentImage:
			total += imageTokenEstimate
		case llm.SegmentFile:
			total += 64 + EstimateTextTokens(segment.Text)
		default:
			total += EstimateTextTokens(segment.Text)
		}
	}
	for _, call := range message.ToolCalls {
		total += MessageTokenOverhead + EstimateTextTokens(call.Name) + EstimateTextTokens(call.Arguments)
	}
	return total
}

func EstimateToolsTokens(tools []llm.ToolSchema) int {
	if len(tools) == 0 {
		return 0
	}
	data, err := json.Marshal(tools)
	if err != nil {
		return len(tools) * 128
	}
	return EstimateTextTokens(string(data))
}

// TruncateTextWithMarker limits text to maxRunes and appends marker when cut.
func TruncateTextWithMarker(text string, maxRunes int, marker string) string {
	if maxRunes <= 0 {
		maxRunes = DefaultUserOriginalMaxRunes
	}
	runes := []rune(strings.TrimSpace(text))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	return string(runes[:maxRunes]) + marker
}

// TruncateTextToTokens keeps a rune prefix that is expected to fit in maxTokens.
// It always keeps at least maxTokens/2 estimated tokens when possible.
func TruncateTextToTokens(text string, maxTokens int) string {
	text = strings.TrimSpace(text)
	if text == "" || maxTokens <= 0 || EstimateTextTokens(text) <= maxTokens {
		return text
	}
	runes := []rune(text)
	lo, hi := 1, len(runes)
	best := 0
	for lo <= hi {
		mid := (lo + hi) / 2
		if EstimateTextTokens(string(runes[:mid])) <= maxTokens {
			best = mid
			lo = mid + 1
		} else {
			hi = mid - 1
		}
	}
	if best <= 0 {
		return ""
	}
	return string(runes[:best])
}
