package teamspawn

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
	"time"
	"unicode/utf16"
)

type TurnFailure struct {
	Reason    string
	Transient bool
	ReasonRaw json.RawMessage
}

var AutoContinueDelays = []time.Duration{15 * time.Second, 45 * time.Second, 120 * time.Second, 300 * time.Second, 600 * time.Second}

func ClassifyTeammateTurnFailure(history []protocol.JSONObject) (*TurnFailure, error) {
	for i := len(history) - 1; i >= 0; i-- {
		row := history[i]
		if row.String("type") != "assistant" {
			continue
		}
		if string(row.Get("isApiErrorMessage")) != "true" {
			return nil, nil
		}
		blocks, err := originalBlocks(row)
		if err != nil {
			return nil, err
		}
		rawText, err := assistantTextRawJSON(blocks)
		if err != nil {
			return nil, err
		}
		text := assistantText(blocks)
		switch text {
		case messages.Interrupt, messages.InterruptForToolUse, "API Error: Request was aborted.", "No response requested.", "The user doesn't want to proceed with this tool use. The tool use was rejected (eg. if it was a file edit, the new_string was NOT written to the file). STOP what you are doing and wait for the user to tell you how to proceed.", "The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed.":
			return nil, nil
		}
		if text == "" {
			text = row.String("errorDetails")
			rawText = row.Get("errorDetails")
			if len(rawText) == 0 || rawText[0] != '"' {
				rawText = []byte(`""`)
			}
		}
		units, err := protocol.DecodeJavaScriptJSONString(rawText)
		if err != nil {
			return nil, err
		}
		reasonUnits := []uint16{}
		for len(units) > 0 {
			split := len(units)
			for i, unit := range units {
				if unit == '\n' {
					split = i
					break
				}
			}
			line := trimTeammateUTF16(units[:split])
			if len(line) > 0 {
				for _, unit := range line {
					if !(unit <= 8 || unit == 11 || unit == 12 || unit >= 14 && unit <= 31) {
						reasonUnits = append(reasonUnits, unit)
					}
				}
				break
			}
			if split == len(units) {
				break
			}
			units = units[split+1:]
		}
		if len(reasonUnits) > 200 {
			reasonUnits = append(reasonUnits[:200], '…')
		}
		if len(reasonUnits) == 0 {
			reasonUnits = utf16.Encode([]rune("API error"))
		}
		reasonRaw := protocol.QuoteJavaScriptUTF16(reasonUnits)
		var reason string
		_ = json.Unmarshal(reasonRaw, &reason)
		return &TurnFailure{Reason: reason, Transient: teamworker.IsTransientTurnFailure(text), ReasonRaw: reasonRaw}, nil
	}
	return nil, nil
}
