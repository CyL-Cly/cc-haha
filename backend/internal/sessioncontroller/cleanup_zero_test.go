package sessioncontroller

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestWriterUsesSourceStrictNumericZeroBeforeNativeTranscriptMaterialization(t *testing.T) {
	for _, tc := range []struct {
		value string
		skip  bool
	}{
		{"0", true}, {"-0", true}, {"0.0", true}, {"0e0", true}, {"0e+20", true},
		{"30", false}, {"\"0\"", false}, {"null", false}, {"false", false},
	} {
		t.Run(tc.value, func(t *testing.T) {
			owner, writer, pending, loader := recordHost(t, OSFiles{}, nil)
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
				cache := writer.options.SettingsState.GetSessionSettingsCache()
				cache.Settings.Set("cleanupPeriodDays", json.RawMessage(tc.value))
				return nil
			})
			ownedAwaitRecord(t, owner, startRecord(t, owner, pending, loader, []LiveRecord{{Record: plainMessage("cleanup-message", "original prompt")}}, RecordOptions{PromptID: func() json.RawMessage { return nil }}))
			ownedFlush(t, owner, pending)
			if tc.skip {
				if _, err := os.Stat(filepath.Join(writer.options.ConfigDir, "projects")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("Source numeric zero must not create transcript directory: %v", err)
				}
			} else if rows := readRows(t, writer.target()); len(rows) != 1 || rows[0].String("uuid") != "cleanup-message" {
				t.Fatalf("nonzero or nonnumeric value disabled persistence: %+v", rows)
			}
		})
	}
}
