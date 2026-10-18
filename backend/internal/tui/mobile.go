package tui

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	qrcode "github.com/skip2/go-qrcode"
)

func TerminalMobilePresentation(platform string) json.RawMessage {
	if platform != "android" {
		platform = "ios"
	}
	address := "https://apps.apple.com/app/claude-by-anthropic/id6473753684"
	if platform == "android" {
		address = "https://play.google.com/store/apps/details?id=com.anthropic.claude"
	}
	value := protocol.NewJSONObject(protocol.JSONField{Name: "platform", Value: rawMobile(platform)}, protocol.JSONField{Name: "url", Value: rawMobile(address)})
	data, _ := value.MarshalJSON()
	return data
}

func rawMobile(value string) json.RawMessage { data, _ := json.Marshal(value); return data }

func (s *State) handleTerminalMobileKey(key Key, now time.Time) (bool, []action) {
	p := s.LocalPanel
	if p == nil || p.props.Kind != "mobile" {
		return false, nil
	}
	name := key.Name
	if name == "" {
		name = key.Text
	}
	actionName := ""
	switch name {
	case "tab", "left", "right":
		actionName = "mobile-switch"
	case "q", "ctrl+c":
		actionName = "mobile-close"
	default:
		result := ResolveChord(keyStroke(key), []string{"Confirmation"}, s.bindings, s.pendingChord)
		if result.Type == "chord_started" {
			s.pendingChord = result.Pending
			s.chordUntil = now.Add(time.Second)
			return true, nil
		}
		s.pendingChord = nil
		if result.Type == "match" && result.Action == "confirm:no" {
			actionName = "mobile-close"
		}
	}
	if actionName != "" {
		return true, []action{{kind: actionLocalPanel, panel: p.result(actionName)}}
	}
	return true, nil
}

// The existing QR encoder generates a real, low-error-correction symbol for
// the same two immutable URLs as Source. The half-block renderer keeps the
// four-module quiet zone; changing terminal width never clips the symbol.
func terminalMobileLines(encoded json.RawMessage, width int) []string {
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(encoded)
	presentation := TerminalMobilePresentation(value.String("platform"))
	_ = value.UnmarshalJSON(presentation)
	code, err := qrcode.New(value.String("url"), qrcode.Low)
	if err != nil {
		return []string{value.String("url")}
	}
	lines := []string{""}
	for _, line := range strings.Split(strings.TrimSuffix(code.ToSmallString(true), "\n"), "\n") {
		lines = append(lines, "  "+line)
	}
	platforms := "[iOS] / Android"
	if value.String("platform") == "android" {
		platforms = "iOS / [Android]"
	}
	lines = append(lines, "", platforms+"  (tab to switch, esc to close)")
	return append(lines, wrap(value.String("url"), max(1, width))...)
}
