package settings

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/migrations"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestRealUserAndManagedWritesKeepJavaScriptNumberAndUTF16Semantics(t *testing.T) {
	for _, managed := range []bool{false, true} {
		options := fixtureOptions(t)
		upgrader, err := migrations.New(migrations.Options{Store: options.Store})
		if err != nil {
			t.Fatal(err)
		}
		options.EnsureMigrated = upgrader.SettingsCallback
		service := mustService(t, options)
		path := filepath.Join(service.configDir(), "settings.json")
		if managed {
			path = filepath.Join(service.configDir(), "cc-haha", "settings.json")
		}
		original := `{"future":{"integer":9007199254740993,"huge":1e400,"negativeHuge":-1e400,"tiny":1e-400,"negativeZero":-0,"high":"\ud800","low":"\udfff"},"unrelated":true}`
		seed(t, path, original)
		if managed {
			_, err = UpdateManaged(testContext, mustManaged(t, options), func(current storage.Object) (ManagedUpdate[bool], error) {
				current["new"] = json.RawMessage(`true`)
				return ManagedUpdate[bool]{Settings: current, Result: true}, nil
			})
		} else {
			err = service.UpdateUserSettings(testContext, object(`{"new":true}`))
		}
		if err != nil {
			t.Fatal(err)
		}
		actual := fileObject(t, path)
		var compact bytes.Buffer
		if err := json.Compact(&compact, actual["future"]); err != nil {
			t.Fatal(err)
		}
		want := `{"integer":9007199254740992,"huge":null,"negativeHuge":null,"tiny":0,"negativeZero":0,"high":"\ud800","low":"\udfff"}`
		if compact.String() != want || string(actual["new"]) != "true" || string(actual["unrelated"]) != "true" {
			t.Fatalf("managed=%v persisted=%s", managed, mustRead(t, path))
		}
	}
}
