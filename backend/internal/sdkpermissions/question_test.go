package sdkpermissions

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestQuestionExplicitNativeAdmissionRetainsDefaultGuard(t *testing.T) {
	owner, settings, gate := setup(t, "")
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, err := Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "darwin", Model: "actual-model", NonInteractive: true, BaseToolsCLI: []string{"Read,AskUserQuestion"}, Allowed: []string{"AskUserQuestion"}, Mode: "bypassPermissions"})
		if err != nil {
			return err
		}
		_, value, err := state.Read(scope)
		if err != nil {
			return err
		}
		var deny map[string][]string
		_ = json.Unmarshal(value.Get("alwaysDenyRules"), &deny)
		for _, name := range deny["cliArg"] {
			if name == "AskUserQuestion" {
				t.Fatal("native Question still denied by partial registry")
			}
		}
		_, err = Initialize(scope, owner, settings, gate, Input{Profile: PublicPrintProfile, NativeProcess: true, Platform: "darwin", Model: "actual-model", NonInteractive: true, Mode: "default"})
		if err == nil {
			t.Fatal("Question opened unfinished complete catalog")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
