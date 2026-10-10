package sdkpermissions

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"strings"
	"testing"
)

func TestPublicPrintPermissionCLIWrite(t *testing.T) {
	owner, settings, gate := setup(t, "")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, e := Initialize(scope, owner, settings, gate, Input{Profile: "source-normal-print-explicit-registry-v1", Platform: "darwin", Model: "fixture-model", NonInteractive: true, BaseToolsCLI: []string{"Write"}, Allowed: []string{"Write"}, Mode: "bypassPermissions"})
		if e != nil {
			return e
		}
		_, ctx, e := state.Read(scope)
		if e != nil {
			return e
		}
		if ctx.String("mode") != "bypassPermissions" {
			t.Fatal(ctx)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("normal public tools/permission input must initialize through registry: %v", err)
	}
}

func TestNativeTodoWriteExplicitCLIListsRetainFiniteBoundary(t *testing.T) {
	for _, bases := range [][]string{{"TodoWrite"}, {"Read,NotebookEdit,TodoWrite"}} {
		owner, settings, gate := setup(t, "")
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			state, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "darwin", Model: "actual-model", NonInteractive: true, BaseToolsCLI: bases, Mode: "default"})
			if err != nil {
				return err
			}
			_, value, err := state.Read(scope)
			if err != nil {
				return err
			}
			var rules map[string][]string
			if err := json.Unmarshal(value.Get("alwaysDenyRules"), &rules); err != nil {
				return err
			}
			for _, rule := range rules["cliArg"] {
				if rule == "TodoWrite" {
					t.Fatal("actual TodoWrite cannot enter CLI catalog")
				}
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	owner, settings, gate := setup(t, "")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, Platform: "darwin", Model: "fixture-model", NonInteractive: true, BaseToolsCLI: []string{"TodoWrite"}, Mode: "default"})
		if err == nil {
			t.Fatal("finite CLI registry was broadened")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeOmittedDefaultCatalogCannotSilentlyDenyUnportedTools(t *testing.T) {
	owner, settings, gate := setup(t, "")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "darwin", Model: "actual-model", NonInteractive: true, Mode: "default"})
		if err == nil || !strings.Contains(err.Error(), "complete default Source tool catalog") {
			t.Fatalf("omitted catalog silently replaced: %v", err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestProvenNativeDefaultDoesNotAddExplicitSubsetDenyRules(t *testing.T) {
	owner, settings, gate := setup(t, "")
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, NativeDefaultCatalogBindings: true, Platform: "darwin", Model: "actual-model", NonInteractive: true, Mode: "default", Allowed: []string{"Read"}, Disallowed: []string{"Write"}})
		if err != nil {
			return err
		}
		_, value, err := state.Read(scope)
		if err != nil {
			return err
		}
		var deny, allow map[string][]string
		if err = json.Unmarshal(value.Get("alwaysDenyRules"), &deny); err != nil {
			return err
		}
		if err = json.Unmarshal(value.Get("alwaysAllowRules"), &allow); err != nil {
			return err
		}
		if len(deny["cliArg"]) != 1 || deny["cliArg"][0] != "Write" || len(allow["cliArg"]) != 1 || allow["cliArg"][0] != "Read" {
			t.Fatal("default added synthetic deny or changed explicit permission intent", deny, allow)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
