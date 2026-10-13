package builtintools

import (
	"encoding/json"

	"github.com/NanmiCoder/cc-haha/backend/internal/claudecodehints"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type ShellHintBindings struct {
	// The binding records a main-thread hint on the same runtime owner.
	Record  func(*cooperative.Scope, claudecodehints.Hint) error
	AgentID string
}

func (s *Service) extractShellHints(scope *cooperative.Scope, stdout json.RawMessage, command string, trimOutput bool) (json.RawMessage, error) {
	if trimOutput {
		var err error
		stdout, err = claudecodehints.StripEmptyLines(stdout)
		if err != nil {
			return nil, err
		}
	}
	extracted, err := claudecodehints.Extract(stdout, quote(command))
	if err != nil {
		return nil, err
	}
	b := s.options.ShellHints
	main := b != nil && b.AgentID == "" && (s.options.BackgroundTasks == nil || s.options.BackgroundTasks.AgentID == "")
	if main && b.Record != nil {
		for _, hint := range extracted.Hints {
			if err = b.Record(scope, hint); err != nil {
				return nil, err
			}
		}
	}
	return extracted.Stripped, nil
}
func (s *Service) stripProcessHints(scope *cooperative.Scope, result *processResult, command string) error {
	units, err := protocol.DecodeJavaScriptJSONString(quote(result.Out))
	if err != nil {
		return err
	}
	end := len(units)
	for end > 0 && protocol.IsJavaScriptWhitespace(rune(units[end-1])) {
		end--
	}
	accumulated := append(append([]uint16{}, units[:end]...), '\n')
	stripped, err := s.extractShellHints(scope, protocol.QuoteJavaScriptUTF16(accumulated), command, true)
	if err != nil {
		return err
	}
	return json.Unmarshal(stripped, &result.Out)
}
