package builtintools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/claudecodehints"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func TestPhysicalShellStripsHintsOnSuccessButKeepsSourceEarlyBashFailures(t *testing.T) {
	service, owner, _, _ := fixture(t)
	records := []claudecodehints.Hint{}
	service.options.ShellHints = &ShellHintBindings{Record: func(scope *cooperative.Scope, h claudecodehints.Hint) error {
		if !scope.BelongsTo(owner) {
			t.Fatal("different owner")
		}
		records = append(records, h)
		return nil
	}}
	for _, agent := range []string{"", "actual-child"} {
		for _, code := range []int{0, 7} {
			command := `printf 'head\n<claude-code-hint v=2 type=plugin value=unsupported/>\n<claude-code-hint v=1 type=plugin value=sample@agent-skills/>\ntail\n'; exit ` + map[int]string{0: "0", 7: "7"}[code]
			if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				before := len(records)
				data, e := service.callBash(context.Background(), input(`{"command":`+string(quote(command))+`}`), queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope, AgentID: agent}})
				text := string(data.Data)
				if code != 0 {
					shell, ok := e.(*toolerrors.ShellError)
					if !ok {
						t.Fatalf("actual failed shell %T %v", e, e)
					}
					text = string(toolerrors.FormatShellError(shell))
				} else if e != nil {
					return e
				}
				if (code == 0 && strings.Contains(text, "<claude-code-hint")) || (code != 0 && !strings.Contains(text, "<claude-code-hint")) || !strings.Contains(text, "head") || !strings.Contains(text, "tail") {
					t.Fatal("model-invisible sidechannel leaked", text)
				}
				want := before
				if agent == "" && code == 0 {
					want++
				}
				if len(records) != want {
					t.Fatal("child/dialog boundary", agent, len(records), want)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
		}
	}
}
func TestRawHintsStripRetainsHalfSurrogatesAndNoUnconditionalRetrim(t *testing.T) {
	service, owner, _, _ := fixture(t)
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		got, e := service.extractShellHints(scope, json.RawMessage(`"<claude-code-hint v=1 type=plugin value=\"\ud800\"/>\n\udc00\n"`), "owned", true)
		if e != nil {
			return e
		}
		if string(got) != `"\n\udc00"` {
			t.Fatal("raw strip or ordering", string(got))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
