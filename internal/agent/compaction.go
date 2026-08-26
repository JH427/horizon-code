package agent

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/JH427/horizon-code/internal/model"
)

const compactSummaryLabel = "COMPACTED_TOOL_HISTORY"

type toolRound struct {
	start int
	end   int
}

func compactMessages(messages []model.Message, recentRounds, maxSummaryBytes int) ([]model.Message, bool) {
	if len(messages) < 3 || maxSummaryBytes < len(compactSummaryLabel) {
		return messages, false
	}
	if recentRounds < 0 {
		recentRounds = 0
	}

	rounds := completeToolRounds(messages)
	compactCount := len(rounds) - recentRounds
	if compactCount <= 0 {
		return messages, false
	}

	removed := make(map[int]bool)
	var entries []string
	for index := 2; index < len(messages); index++ {
		message := messages[index]
		if message.Role == "user" && strings.HasPrefix(message.Content, compactSummaryLabel) {
			removed[index] = true
			entries = append(entries, "- prior="+safeCompactText(strings.TrimPrefix(message.Content, compactSummaryLabel), 200))
		}
	}
	for _, round := range rounds[:compactCount] {
		assistant := messages[round.start]
		for offset, call := range assistant.ToolCalls {
			result := messages[round.start+1+offset]
			entries = append(entries, fmt.Sprintf("- tool=%s args=%s outcome=%s",
				safeCompactText(call.Function.Name, 80),
				safeCompactText(call.Function.Arguments, 160),
				safeCompactText(result.Content, 120),
			))
		}
		for index := round.start; index < round.end; index++ {
			removed[index] = true
		}
	}

	summary := compactSummaryLabel + "\nsource=older complete assistant/tool rounds\n" + strings.Join(entries, "\n")
	summary = safeCompactText(summary, maxSummaryBytes)
	result := make([]model.Message, 0, len(messages))
	inserted := false
	for index, message := range messages {
		if removed[index] {
			if !inserted {
				result = append(result, model.Message{Role: "user", Content: summary})
				inserted = true
			}
			continue
		}
		result = append(result, message)
	}
	return result, true
}

func completeToolRounds(messages []model.Message) []toolRound {
	var rounds []toolRound
	for index := 2; index < len(messages); index++ {
		assistant := messages[index]
		if assistant.Role != "assistant" || len(assistant.ToolCalls) == 0 || index+len(assistant.ToolCalls) >= len(messages) {
			continue
		}
		complete := true
		for offset, call := range assistant.ToolCalls {
			result := messages[index+1+offset]
			if result.Role != "tool" || result.ToolCallID != call.ID {
				complete = false
				break
			}
		}
		if complete {
			rounds = append(rounds, toolRound{start: index, end: index + 1 + len(assistant.ToolCalls)})
			index += len(assistant.ToolCalls)
		}
	}
	return rounds
}

func safeCompactText(value string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	const truncationMarker = "[truncated]"
	clean := strings.ToValidUTF8(value, "?")
	truncated := len(clean) > maxBytes
	limit := maxBytes
	if truncated && maxBytes > len(truncationMarker) {
		limit -= len(truncationMarker)
	}
	var builder strings.Builder
	for _, r := range clean {
		if unicode.IsControl(r) {
			if r == '\n' || r == '	' || r == '\r' {
				r = ' '
			} else {
				r = '?'
			}
		}
		if builder.Len()+utf8.RuneLen(r) > limit {
			truncated = true
			break
		}
		builder.WriteRune(r)
	}
	if truncated && maxBytes >= len(truncationMarker) {
		builder.WriteString(truncationMarker)
	}
	return builder.String()
}
