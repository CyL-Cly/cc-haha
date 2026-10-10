package sdkpermissions

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestImagesRequireActualBindingAndEnabledCatalogProjection(t *testing.T) {
	for _, bound := range []bool{false, true} {
		actual, err := nativeToolList([]string{"ImageGen(*),ImageEdit(user path),Read"}, false, false, false, false, false, false, false, false, false, false, false, false, bound)
		if !bound && err == nil || bound && (err != nil || !reflect.DeepEqual(actual, []string{"ImageGen", "ImageEdit(user path)", "Read"})) {
			t.Fatal("same owner image binding proof", bound, actual, err)
		}
	}
	for _, enabled := range []bool{false, true} {
		owner, settings, gate := setup(t, "")
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			input := Input{Profile: PublicPrintProfile, NativeProcess: true, NativeImageBindings: true, NativeImageEnabled: enabled, Platform: "darwin", Model: "fixture-model", NonInteractive: true, BaseToolsCLI: []string{"Read"}, Mode: "default"}
			state, err := Initialize(scope, owner, settings, gate, input)
			if err != nil {
				return err
			}
			_, permission, err := state.Read(scope)
			if err != nil {
				return err
			}
			var denied map[string][]string
			_ = json.Unmarshal(permission.Get("alwaysDenyRules"), &denied)
			for _, name := range []string{"ImageGen", "ImageEdit"} {
				found := false
				for _, rule := range denied["cliArg"] {
					found = found || rule == name
				}
				if found != enabled {
					return fmt.Errorf("image CLI mask enabled=%v actualdeny=%v tool=%s", enabled, found, name)
				}
			}
			if enabled {
				input.NativeImageBindings = false
				if _, err := Initialize(scope, owner, settings, gate, input); err == nil {
					return fmt.Errorf("enabled image tools accepted without real native producer")
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}
