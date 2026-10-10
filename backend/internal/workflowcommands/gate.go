package workflowcommands

import (
	"bytes"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

type Gate struct {
	owner       *cooperative.Owner
	settings    *clistartupenv.Service
	environment *clistartupenv.Environment
}

func NewGate(owner *cooperative.Owner, settings *clistartupenv.Service, environment *clistartupenv.Environment) (*Gate, error) {
	if owner == nil || settings == nil || environment == nil {
		return nil, cooperative.ErrRestricted
	}
	return &Gate{owner, settings, environment}, nil
}
func (g *Gate) DisabledReason(scope *cooperative.Scope) (string, error) {
	if g == nil || !scope.BelongsTo(g.owner) {
		return "", cooperative.ErrRestricted
	}
	raw := g.environment.Get("CLAUDE_CODE_DISABLE_WORKFLOWS")
	if bytes.Equal(raw, []byte("true")) {
		return "env", nil
	}
	if raw != nil && !bytes.Equal(raw, []byte("false")) && !bytes.Equal(raw, []byte("null")) && !bytes.Equal(raw, []byte("0")) {
		units, err := protocol.DecodeJavaScriptJSONString(raw)
		if err != nil {
			return "", &CapabilityError{"workflow env string"}
		}
		normalized := strings.ToLower(stringUTF16ASCII(trimUnits(units)))
		switch normalized {
		case "1", "true", "yes", "on":
			return "env", nil
		}
	}
	policy, err := g.settings.GetSettingsForSource("policySettings")
	if err != nil {
		return "", err
	}
	if policy != nil && bytes.Equal(policy.Get("disableWorkflows"), []byte("true")) {
		return "managed", nil
	}
	merged, err := g.settings.GetInitialSettings()
	if err != nil {
		return "", err
	}
	if merged != nil && (bytes.Equal(merged.Get("disableWorkflows"), []byte("true")) || bytes.Equal(merged.Get("enableWorkflows"), []byte("false"))) {
		return "settings", nil
	}
	return "", nil
}
