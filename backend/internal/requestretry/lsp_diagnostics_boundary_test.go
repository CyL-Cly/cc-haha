package requestretry

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestLSPRetainedDiagnosticsCannotWidenFiniteRequestProfile(t *testing.T) {
	input := standardInput()
	input.Context.History = []protocol.JSONObject{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"attachment"`)}, protocol.JSONField{Name: "attachment", Value: json.RawMessage(`{"type":"diagnostics","files":[],"isNew":true}`)})}
	err := validated(input, standardDeps())
	var failure *CapabilityError
	if !errors.As(err, &failure) || failure.Boundary != "native LSP diagnostic attachment renderer" {
		t.Fatal("finite request admitted native diagnostics", err)
	}
}
