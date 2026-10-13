package requestretry

import (
	"bytes"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestNativeMemorySavedCompletionHistoryAdmitsOnlyItsActualHostClass(t *testing.T) {
	for _, row := range []struct {
		name, record string
		allow        bool
	}{
		{"actual", `{"type":"system","subtype":"memory_saved","uuid":"actual-memory","timestamp":"2026-10-13T00:00:00Z","isMeta":false,"writtenPaths":["/private/memory/MEMORY.md"],"verb":"Improved","future":{"kept":true}}`, true},
		{"empty", `{"type":"system","subtype":"memory_saved","uuid":"actual-memory","timestamp":"2026-10-13T00:00:00Z","isMeta":false,"writtenPaths":[]}`, true},
		{"surrogate", `{"type":"system","subtype":"memory_saved","uuid":"actual-memory","timestamp":"2026-10-13T00:00:00Z","isMeta":false,"writtenPaths":["/memory/\ud800.md"]}`, true},
		{"malformed-path", `{"type":"system","subtype":"memory_saved","uuid":"actual-memory","timestamp":"2026-10-13T00:00:00Z","isMeta":false,"writtenPaths":[{}]}`, false},
		{"missing-identity", `{"type":"system","subtype":"memory_saved","isMeta":false,"writtenPaths":["/memory"]}`, false},
		{"virtual", `{"type":"system","subtype":"memory_saved","uuid":"actual-memory","timestamp":"2026-10-13T00:00:00Z","isMeta":false,"writtenPaths":[],"isVirtual":false}`, false},
	} {
		t.Run(row.name, func(t *testing.T) {
			input, deps := standardInput(), standardDeps()
			input.Context.Host.Profile = requestcontext.NativeDirectRelayProfile
			record := originalHistoryRecord(t, row.record)
			before, _ := record.MarshalJSON()
			input.Context.History = append([]protocol.JSONObject{record}, input.Context.History...)
			err := validated(input, deps)
			if (err == nil) != row.allow {
				t.Fatalf("native completion admission allow=%t error=%v", row.allow, err)
			}
			after, _ := record.MarshalJSON()
			if !bytes.Equal(before, after) {
				t.Fatal("admission mutated authoritative transcript")
			}
		})
	}
}
