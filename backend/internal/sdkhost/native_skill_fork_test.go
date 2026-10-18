package sdkhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestForkSkillTransferCommandRulesRetainProofPolicyAndIndependentValues(t *testing.T) {
	rules := []json.RawMessage{quote("Write"), quote("Read"), quote("Write")}
	if _, err := (NativeSettingsTransfer{}).WithCommandRules(rules); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("unminted transfer acquired proof", err)
	}
	settings := object([]byte(`{"model":"same"}`))
	permission := object([]byte(`{"mode":"default","alwaysAllowRules":{"cliArg":["Read"],"command":["Bash"]},"alwaysDenyRules":{"policySettings":["Write(secret)"]},"unknown":{"keep":true}}`))
	proof := &nativeSettingsProof{sameReference: true}
	before := NativeSettingsTransfer{settings: settings, merged: settings, permission: permission, proof: proof}
	after, err := before.WithCommandRules(rules)
	if err != nil {
		t.Fatal(err)
	}
	if after.proof != proof || after.proof.sameReference != before.proof.sameReference {
		t.Fatal("original reference proof changed")
	}
	if string(after.permission.Get("alwaysAllowRules")) != `{"cliArg":["Read"],"command":["Bash","Write","Read"]}` {
		t.Fatal("command Set semantics changed", string(after.permission.Get("alwaysAllowRules")))
	}
	for _, key := range []string{"mode", "alwaysDenyRules", "unknown"} {
		if string(after.permission.Get(key)) != string(permission.Get(key)) {
			t.Fatal("live deny/provenance weakened", key)
		}
	}
	if string(before.permission.Get("alwaysAllowRules")) != `{"cliArg":["Read"],"command":["Bash"]}` {
		t.Fatal("parent transfer mutated")
	}
	// A later parent policy snapshot remains authoritative: the child overlay
	// carries only command grants, never replaces explicit deny or other sources.
	permission.Set("alwaysDenyRules", []byte(`{"policySettings":["Write"]}`))
	later := before
	later.permission = permission
	laterChild, err := later.WithCommandRules(rules)
	if err != nil || string(laterChild.permission.Get("alwaysDenyRules")) != `{"policySettings":["Write"]}` {
		t.Fatal("live policy refresh lost", err)
	}
	if _, err := before.WithCommandRules([]json.RawMessage{json.RawMessage(`{}`)}); err == nil {
		t.Fatal("non-string permission rule accepted")
	}
	owner := cooperative.New()
	defer owner.Close()
	h := &Host{options: Options{Owner: owner, NativeParentSettings: func(*cooperative.Scope) (NativeSettingsTransfer, error) { return NativeSettingsTransfer{}, nil }}}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, _, err := h.readASKSettings(scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("fork helper bypassed original unminted guard: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestForkSkillCatalogCopiesOnlyInstalledToolsAndRejectsForeignRetainedClosedScopes(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	defer owner.Close()
	defer foreign.Close()
	h := &Host{options: Options{Owner: owner, Print: &PrintOptions{NativeProcess: true}}, registered: []queryloop.RegisteredTool{{Name: "Read"}, {Name: "mcp__fixture__tool"}}}
	if _, err := h.NativeSkillForkToolNames(nil); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal(err)
	}
	if err := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, err := h.NativeSkillForkToolNames(scope)
		if !errors.Is(err, cooperative.ErrRestricted) {
			return fmt.Errorf("foreign catalog scope: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var retained *cooperative.Scope
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		retained = scope
		names, err := h.NativeSkillForkToolNames(scope)
		if err != nil || len(names) != 2 || names[0] != "Read" || names[1] != "mcp__fixture__tool" {
			return fmt.Errorf("catalog=%v error=%v", names, err)
		}
		names[0] = "Write"
		if h.registered[0].Name != "Read" {
			return errors.New("catalog alias exposed")
		}
		h.closing = true
		_, err = h.NativeSkillForkToolNames(scope)
		if !errors.Is(err, cooperative.ErrClosed) {
			return fmt.Errorf("closed host admitted: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := h.NativeSkillForkToolNames(retained); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("retained scope accepted", err)
	}
}
