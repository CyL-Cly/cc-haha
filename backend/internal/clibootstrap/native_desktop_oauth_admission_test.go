package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// Optional Claude auth must stay absent on the original offline/public print
// graph. Selecting that domain still requires its real auth producer and owner.
func TestNativeClaudeOptionalDomainKeepsOfflinePrintAndRejectsSelectedMissingProducer(t *testing.T) {
	native := nativeForTest(t)
	arguments, err := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if err != nil {
		t.Fatal(err)
	}
	var output, stderr bytes.Buffer
	main, err := NewMainPrint(MainPrintOptions{Input: strings.NewReader(""), Output: &output, Error: &stderr, Arguments: arguments, Native: native, PID: os.Getpid(), Version: "fixture", Platform: "darwin"})
	if err != nil {
		t.Fatal(err)
	}
	defer main.Close()
	if native.NativeProcess || main.openAIAuth != nil {
		t.Fatal("test must retain original offline graph without native auth")
	}
	var stale *cooperative.Scope
	if err = main.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		stale = scope
		binding, err := main.nativeClaudeAuthBinding(scope)
		if err != nil || binding != nil {
			return errors.New("unselected optional Claude domain rejected offline print")
		}
		if err = main.environment.SetProperty(quoteMain("CLAUDE_CODE_ENTRYPOINT"), quoteMain("claude-desktop")); err != nil {
			return err
		}
		binding, err = main.nativeClaudeAuthBinding(scope)
		if !errors.Is(err, cooperative.ErrRestricted) || binding != nil {
			return errors.New("selected Claude domain accepted missing real auth producer")
		}
		return main.environment.SetProperty(quoteMain("CLAUDE_CODE_ENTRYPOINT"), nil)
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = main.nativeClaudeAuthBinding(stale); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("stale owner accepted", err)
	}
	foreign := cooperative.New()
	defer foreign.Close()
	if err = foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		if binding, err := main.nativeClaudeAuthBinding(scope); !errors.Is(err, cooperative.ErrRestricted) || binding != nil {
			return errors.New("foreign owner accepted before optional-domain selection")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err = main.nativeClaudeAuthBinding(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("nil owner accepted", err)
	}
}
