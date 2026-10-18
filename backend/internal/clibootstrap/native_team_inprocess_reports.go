package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamspawn"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
	"strings"
	"unicode"
	"unicode/utf16"
)

func (m *MainPrint) takeNativeInProcessNextPrompt(ctx context.Context, taskID string, next protocol.JSONObject) (string, error) {
	current := ""
	switch next.String("type") {
	case "aborted", "timeout", "cancelled":
		return "", nil
	case "new_message":
		if next.String("from") == "user" {
			return next.String("message"), nil
		}
		current = teamspawn.TeammateMessage(next.String("from"), next.String("message"), next.String("color"), next.String("summary"))
	case "new_messages":
		var messages []protocol.JSONObject
		if err := json.Unmarshal(next.Get("messages"), &messages); err != nil {
			return "", err
		}
		current = teamstore.FormatMessages(messages)
	case "shutdown_request":
		current = teamspawn.TeammateMessage(next.String("from"), next.String("originalMessage"), "", "")
	default:
		return "", errors.New("Invalid original teammate wait result")
	}
	return current, m.appendNativeInProcessPrompt(ctx, taskID, current)
}
func nativeTeammateFailureNote(reason, note string) string {
	max := 200 - len(utf16.Encode([]rune(note))) - 1
	units := utf16.Encode([]rune(reason))
	if len(units) > max {
		units = append(units[:max-1], '…')
	}
	return string(utf16.Decode(units)) + " " + note
}
func (m *MainPrint) sendNativeInProcessTurnReport(ctx context.Context, identity sessioncontroller.InProcessTeammateIdentity, history []protocol.JSONObject, idleReason, failureReason, result string, hasResult bool, failureReasons ...json.RawMessage) error {
	stamp := m.options.Native.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	notification := nativeInProcessObject(rawMain(map[string]string{"type": "idle_notification", "from": identity.AgentName, "timestamp": stamp}))
	if idleReason != "" {
		notification.Set("idleReason", quoteMain(idleReason))
	} else if failureReason != "" {
		notification.Set("idleReason", quoteMain("failed"))
	}
	if failureReason != "" {
		raw := quoteMain(failureReason)
		if len(failureReasons) > 0 && failureReasons[0] != nil {
			raw = failureReasons[0]
		}
		capped, err := nativeCapIdleFailureRawJSON(raw)
		if err != nil {
			return err
		}
		if string(capped) != `""` {
			notification.Set("failureReason", capped)
		}
	}
	if hasResult {
		raw, found, err := teamspawn.TeammateTurnResultRawJSON(history)
		if err != nil {
			return err
		}
		if !found {
			raw = quoteMain(result)
		}
		capped, err := nativeCapIdleResultRawJSON(raw, idleReason != "failed" && failureReason == "")
		if err != nil {
			return err
		}
		if string(capped) != `""` {
			notification.Set("result", capped)
		}
	}
	summary, err := teamspawn.LastPeerDMSummary(history)
	if err != nil {
		return err
	}
	notification.Set("summary", summary)
	envelope := nativeInProcessObject(rawMain(map[string]string{"from": identity.AgentName, "text": string(rawMain(notification)), "timestamp": stamp}))
	if identity.Color != "" {
		envelope.Set("color", quoteMain(identity.Color))
	}
	mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: m.options.Native.ConfigHomeDirectory(), TaskStore: m.taskStore})
	if err != nil {
		return err
	}
	return mailbox.Write("team-lead", identity.TeamName, envelope)
}

func nativeStripIdleControls(text string) string {
	return strings.Map(func(r rune) rune {
		if r <= 8 || r >= 11 && r <= 31 || r >= 127 && r <= 159 || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Cf, r) && r != 0x200c && r != 0x200d {
			return -1
		}
		return r
	}, text)
}
func nativeTruncateIdleText(text string, max int) string {
	units := utf16.Encode([]rune(text))
	if len(units) <= max {
		return text
	}
	units = units[:max]
	if len(units) > 0 && units[len(units)-1] >= 0xd800 && units[len(units)-1] <= 0xdbff {
		units = units[:len(units)-1]
	}
	return string(utf16.Decode(units))
}
func nativeCapIdleResult(text string, reachable bool) string {
	text = protocol.TrimJavaScriptString(nativeStripIdleControls(protocol.TrimJavaScriptString(text)))
	if text == "" {
		return ""
	}
	head := nativeTruncateIdleText(text, 4000)
	if head == text {
		return text
	}
	marker := "[result truncated]"
	if reachable {
		marker = "[result truncated — ask the agent for the rest via SendMessage]"
	}
	return head + "\n" + marker
}
func nativeCapIdleFailure(text string) string {
	text = protocol.TrimJavaScriptString(text)
	for _, line := range strings.Split(text, "\n") {
		if protocol.TrimJavaScriptString(line) != "" {
			return nativeTruncateIdleText(protocol.TrimJavaScriptString(nativeStripIdleControls(line)), 200)
		}
	}
	return ""
}

// Raw JSON preserves JavaScript UTF-16, including unpaired surrogates in a
// provider's text. Notification caps count code units just as MAIN does.
func nativeIdleUnits(raw json.RawMessage) ([]uint16, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, err
	}
	units = nativeTrimIdleUnits(units)
	clean := make([]uint16, 0, len(units))
	for i := 0; i < len(units); i++ {
		r := rune(units[i])
		width := 1
		if r >= 0xd800 && r <= 0xdbff && i+1 < len(units) && units[i+1] >= 0xdc00 && units[i+1] <= 0xdfff {
			r = utf16.DecodeRune(r, rune(units[i+1]))
			width = 2
		}
		if !(r <= 8 || r >= 11 && r <= 31 || r >= 127 && r <= 159 || r == 0x2028 || r == 0x2029 || unicode.Is(unicode.Cf, r) && r != 0x200c && r != 0x200d) {
			clean = append(clean, units[i:i+width]...)
		}
		i += width - 1
	}
	return nativeTrimIdleUnits(clean), nil
}
func nativeTrimIdleUnits(units []uint16) []uint16 {
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[0])) {
		units = units[1:]
	}
	for len(units) > 0 && protocol.IsJavaScriptWhitespace(rune(units[len(units)-1])) {
		units = units[:len(units)-1]
	}
	return units
}
func nativeCapIdleResultRawJSON(raw json.RawMessage, reachable bool) (json.RawMessage, error) {
	units, err := nativeIdleUnits(raw)
	if err != nil {
		return nil, err
	}
	if len(units) <= 4000 {
		return protocol.QuoteJavaScriptUTF16(units), nil
	}
	units = units[:4000]
	if units[len(units)-1] >= 0xd800 && units[len(units)-1] <= 0xdbff {
		units = units[:len(units)-1]
	}
	marker := "\n[result truncated]"
	if reachable {
		marker = "\n[result truncated — ask the agent for the rest via SendMessage]"
	}
	return protocol.QuoteJavaScriptUTF16(append(units, utf16.Encode([]rune(marker))...)), nil
}
func nativeCapIdleFailureRawJSON(raw json.RawMessage) (json.RawMessage, error) {
	units, err := protocol.DecodeJavaScriptJSONString(raw)
	if err != nil {
		return nil, err
	}
	units = nativeTrimIdleUnits(units)
	for i, unit := range units {
		if unit == '\n' {
			units = units[:i]
			break
		}
	}
	clean, err := nativeIdleUnits(protocol.QuoteJavaScriptUTF16(units))
	if err != nil {
		return nil, err
	}
	if len(clean) > 200 {
		clean = clean[:200]
		if clean[len(clean)-1] >= 0xd800 && clean[len(clean)-1] <= 0xdbff {
			clean = clean[:len(clean)-1]
		}
	}
	return protocol.QuoteJavaScriptUTF16(clean), nil
}

func nativeTeammateFailureNoteRawJSON(raw json.RawMessage, note string) json.RawMessage {
	units, _ := protocol.DecodeJavaScriptJSONString(raw)
	suffix := utf16.Encode([]rune(" " + note))
	room := 200 - len(suffix)
	if len(units) > room {
		units = append(units[:room-1], '…')
	}
	return protocol.QuoteJavaScriptUTF16(append(units, suffix...))
}
