package imruntime

import "github.com/NanmiCoder/cc-haha/backend/internal/protocol"

type StreamingChunk struct {
	Text           Text `json:"text"`
	Remainder      Text `json:"remainder"`
	ConsumedLength int  `json:"consumedLength"`
}
type StreamingPlan struct {
	Sealed []Text `json:"sealedChunks"`
	Active Text   `json:"activeChunk"`
}
type ThinkingUpdate struct {
	FullText    Text `json:"fullText"`
	MessageText Text `json:"messageText"`
}

func TelegramOutbound(text Text) Text  { return ConvertMarkdownTables(text) }
func TelegramStreaming(text Text) Text { return TelegramOutbound(text).Join(NewText(" ▍")) }
func TelegramThinking(current, delta Text, limit int) ThinkingUpdate {
	full := current.Join(delta)
	if limit < 0 {
		limit = 0
	}
	preview := full.Slice(0, limit).TrimStart()
	message := NewText("💭 思考中...")
	if len(preview) > 0 {
		message = NewText("💭 ").Join(preview).Join(NewText("..."))
	}
	return ThinkingUpdate{full, message}
}
func SplitTelegramStreaming(text Text, limit int) StreamingChunk {
	rough := limit
	if rough > len(text) {
		rough = len(text)
	}
	build := func(sealed Text, split int) StreamingChunk {
		consumed := split
		for consumed < len(text) && protocol.IsJavaScriptWhitespace(rune(text[consumed])) {
			consumed++
		}
		return StreamingChunk{sealed, text.Slice(consumed, len(text)), consumed}
	}
	for _, delimiter := range []string{"\n\n", "\n", ". ", " "} {
		candidate := text.LastIndex(NewText(delimiter), rough)
		if candidate <= 0 {
			continue
		}
		split := candidate
		if text[split] == '\n' || text[split] == '.' {
			split++
		}
		sealed := text.Slice(0, split).TrimEnd()
		if len(sealed) > 0 && len(TelegramOutbound(sealed)) <= limit {
			return build(sealed, split)
		}
	}
	chunks := SplitMessage(TelegramOutbound(text), limit)
	first := text.Slice(0, limit)
	if len(chunks) > 0 {
		first = chunks[0]
	}
	if len(first) < len(text) && text.HasPrefix(first) {
		return build(first.TrimEnd(), len(first))
	}
	split := limit
	if split > len(text) {
		split = len(text)
	}
	if split < 1 {
		split = 1
	}
	return build(text.Slice(0, split).TrimEnd(), split)
}
func PlanTelegramStreaming(current, delta Text, limit int) StreamingPlan {
	full := current.Join(delta)
	plan := StreamingPlan{Sealed: []Text{}, Active: full}
	for len(TelegramOutbound(plan.Active)) > limit {
		chunk := SplitTelegramStreaming(plan.Active, limit)
		plan.Sealed = append(plan.Sealed, chunk.Text)
		plan.Active = chunk.Remainder
		if len(plan.Active) == 0 {
			break
		}
	}
	return plan
}
