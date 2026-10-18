package application

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var terminalTeamsModelEnvelope = regexp.MustCompile(`(?s)^<teammate-message teammate_id="([^"]+)"(?: color="[^"]+")?>\n(.*?)\n</teammate-message>`)

// This fixture counts only a new incoming user turn. Searching the entire
// request would count prior notifications again on every ordinary ASK.
func terminalTeamsModelNotifications(request protocol.JSONObject) ([]string, bool, error) {
	var messages []protocol.JSONObject
	if err := json.Unmarshal(request.Get("messages"), &messages); err != nil || len(messages) == 0 {
		return nil, false, fmt.Errorf("actual model messages unavailable: %v", err)
	}
	last := messages[len(messages)-1]
	if last.String("role") != "user" {
		return nil, false, fmt.Errorf("unexpected final model role %q", last.String("role"))
	}
	text := last.String("content")
	if text == "" {
		var blocks []protocol.JSONObject
		if err := json.Unmarshal(last.Get("content"), &blocks); err != nil {
			return nil, false, err
		}
		for _, block := range blocks {
			if block.String("type") != "text" {
				return nil, false, nil
			}
			text += block.String("text")
		}
	}
	text = protocol.TrimJavaScriptString(text)
	if !strings.HasPrefix(text, "<teammate-message") {
		return nil, false, nil
	}
	keys := []string{}
	for text != "" {
		match := terminalTeamsModelEnvelope.FindStringSubmatchIndex(text)
		if match == nil {
			return nil, true, fmt.Errorf("new incoming turn contains non-teammate content: %q", text)
		}
		sender := text[match[2]:match[3]]
		var notification protocol.JSONObject
		if err := json.Unmarshal([]byte(text[match[4]:match[5]]), &notification); err != nil {
			return nil, true, err
		}
		key := ""
		switch notification.String("type") {
		case "idle_notification":
			if sender != "one" && sender != "two" && sender != "three" || notification.String("from") != sender || notification.String("idleReason") != "available" {
				return nil, true, fmt.Errorf("unexpected actual idle notification %s", raw(notification))
			}
			key = "idle:" + sender
		case "teammate_terminated":
			if sender != "system" {
				return nil, true, fmt.Errorf("termination sender %q", sender)
			}
			switch notification.String("message") {
			case "one has shut down.":
				key = "terminated:one"
			case "two was terminated.":
				key = "terminated:two"
			case "three was terminated.":
				key = "terminated:three"
			default:
				return nil, true, fmt.Errorf("unexpected actual termination %s", raw(notification))
			}
		case "shutdown_approved":
			if sender != "one" || notification.String("from") != "one" || notification.String("requestId") == "" || notification.String("paneId") == "" || notification.String("backendType") != "tmux" {
				return nil, true, fmt.Errorf("unexpected actual shutdown approval %s", raw(notification))
			}
			key = "approved:one"
		default:
			return nil, true, fmt.Errorf("unexpected actual inbox model notification %s", raw(notification))
		}
		keys = append(keys, key)
		text = protocol.TrimJavaScriptString(text[match[1]:])
	}
	return keys, true, nil
}

func TestPublicTeamsIncomingRequestClassifierRequiresNewCompleteKnownEnvelopes(t *testing.T) {
	incoming := `<teammate-message teammate_id="one" color="blue">` + "\n" + `{"type":"idle_notification","from":"one","idleReason":"available"}` + "\n</teammate-message>"
	request := func(content any) protocol.JSONObject {
		return obj(raw(map[string]any{"messages": []any{map[string]any{"role": "user", "content": incoming}, map[string]any{"role": "assistant", "content": "previous notification processed"}, map[string]any{"role": "user", "content": content}}}))
	}
	keys, isIncoming, err := terminalTeamsModelNotifications(request(incoming))
	if err != nil || !isIncoming || len(keys) != 1 || keys[0] != "idle:one" {
		t.Fatal("actual isolated new incoming turn", keys, isIncoming, err)
	}
	for _, content := range []any{"ordinary next human", []any{map[string]any{"type": "tool_result", "content": incoming, "tool_use_id": "real-tool"}}} {
		keys, isIncoming, err := terminalTeamsModelNotifications(request(content))
		if err != nil || isIncoming || len(keys) != 0 {
			t.Fatal("historical or tool text counted as a new incoming query", keys, isIncoming, err)
		}
	}
	for _, text := range []string{incoming + "\nordinary human", strings.Replace(incoming, `"from":"one"`, `"from":"unrelated"`, 1), strings.Replace(incoming, "idle_notification", "unrecognized_type", 1), strings.Replace(incoming, "</teammate-message>", "", 1)} {
		_, _, err := terminalTeamsModelNotifications(request(text))
		if err == nil {
			t.Fatal("invalid notification query was admitted", text)
		}
	}
}
