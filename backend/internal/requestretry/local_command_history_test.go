package requestretry

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

// Actual Source local stdout remains original host history until Prepare merges
// it into user history. This must reach the existing request producer unchanged.
func TestOriginalLocalCommandHistoryAdmission(t *testing.T) {
	input, deps := standardInput(), standardDeps()
	local := originalHistoryRecord(t, `{"type":"system","subtype":"local_command","content":"<local-command-stdout>13 input, 7 output</local-command-stdout>","uuid":"local-id","timestamp":"2023-11-14T22:13:20.000Z","isMeta":false}`)
	input.Context.History = append([]protocol.JSONObject{local}, input.Context.History...)
	if e := validated(input, deps); e != nil {
		t.Fatalf("real local command should reach Prepare: %v", e)
	}
	if local.String("type") != "system" {
		t.Fatal("admission rewrote caller history")
	}
}
