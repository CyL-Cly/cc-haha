package sdkhost

import (
	"context"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"testing"
)

func TestNeutralEnvironmentNoHiddenRegistryFlags(t *testing.T) {
	original := map[string]string{"CLAUDE_CODE_SIMPLE": "1", "CLAUDE_CODE_ENTRYPOINT": "sdk"}
	object := protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote("/private/original")}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote("/private/original/.claude")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}, protocol.JSONField{Name: "CLAUDE_CODE_ENTRYPOINT", Value: quote("sdk")})
	env, e := clistartupenv.NewEnvironment(object)
	if e != nil {
		t.Fatal(e)
	}
	if e = validateNeutralEnvironment(env, original); e != nil {
		t.Fatal(e)
	}
	_ = env.SetProperty(quote("ENABLE_LSP_TOOL"), quote("1"))
	if e = validateNeutralEnvironment(env, original); e == nil {
		t.Fatal("hidden active registry flag admitted")
	}
	_ = object.SetPropertyJSON([]byte(`"\ud800"`), []byte(`null`))
	env, e = clistartupenv.NewEnvironment(object)
	if e != nil {
		t.Fatal(e)
	}
	if e = validateNeutralEnvironment(env, original); e == nil {
		t.Fatal("unknown UTF16 env admitted")
	}
	env, _ = clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: []byte(`null`)}))
	if e = validateNeutralEnvironment(env, original); e == nil {
		t.Fatal("non-string filesystem primitive admitted")
	}
	object.Delete("CLAUDE_CODE_SIMPLE")
	_ = object.SetPropertyJSON([]byte(`"\ud800"`), nil)
	env, _ = clistartupenv.NewEnvironment(object)
	if e = validateNeutralEnvironment(env, original); e == nil {
		t.Fatal("same-size alternate flag identity admitted")
	}
	if e = validateNeutralEnvironment(nil, original); e == nil {
		t.Fatal("nil env")
	}
}
func TestNeutralOwnedConstructorRejectsBeforeServices(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	var retained *cooperative.Scope
	if _, e := NewOwnedNeutral(nil, Options{Owner: owner}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(s *cooperative.Scope) error {
		retained = s
		_, e := NewOwnedNeutral(s, Options{Owner: owner})
		var cap *CapabilityError
		if !errors.As(e, &cap) || cap.Boundary != "shared settings construction identity" {
			return fmt.Errorf("%s", fmt.Sprint(e))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := NewOwnedNeutral(retained, Options{Owner: owner}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal("stale scope reached services", e)
	}
}
func TestNeutralEntrypointUsesDeclaredOriginalPrimitive(t *testing.T) {
	raw := map[string]string{"CLAUDE_CODE_ENTRYPOINT": "sdk"}
	if _, e := originalRequestEnvironment(raw); e == nil {
		t.Fatal("legacy original cap widened")
	}
	out, e := requestEnvironmentForProfile(raw, true)
	if e != nil || out["CLAUDE_CODE_ENTRYPOINT"] != "sdk" {
		t.Fatal("original billing entrypoint omitted", out, e)
	}
	for _, v := range []string{"", "cli", "sdk-cli", "unknown"} {
		if _, e := requestEnvironmentForProfile(map[string]string{"CLAUDE_CODE_ENTRYPOINT": v}, true); e == nil {
			t.Fatal("unported entrypoint broadened", v)
		}
	}
	if _, e := requestEnvironmentForProfile(map[string]string{"CLAUDE_CODE_EXPERIMENTAL_AGENT_TEAMS": "1"}, true); e == nil {
		t.Fatal("active env slipped through new profile")
	}
}
