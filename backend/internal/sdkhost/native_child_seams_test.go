package sdkhost

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestOriginalChildBindingRetainsOwnershipAndMainDefault(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	original, e := modelaccounting.NewAgentContext(owner, protocol.NewJSONObject(protocol.JSONField{Name: "agentId", Value: quote("actual-child-id")}))
	if e != nil {
		t.Fatal(e)
	}
	got, e := originalAgentContext(owner, original)
	if e != nil || got != original {
		t.Fatalf("original child context was replaced: %v", e)
	}
	if _, e = originalAgentContext(foreign, original); e == nil || !strings.Contains(e.Error(), "owner identity") {
		t.Fatalf("foreign child context accepted: %v", e)
	}
	main, e := originalAgentContext(owner, nil)
	if e != nil || main == nil || !main.Owns(owner) {
		t.Fatal(e)
	}
	for _, h := range []*Host{{}, {options: Options{Print: &PrintOptions{}}}} {
		if h.querySource() != "sdk" {
			t.Fatal("old default source changed")
		}
	}
	h := &Host{options: Options{Print: &PrintOptions{NativeProcess: true, QuerySource: "agent:general-purpose"}}}
	if h.querySource() != "agent:general-purpose" {
		t.Fatal("actual child querySource lost")
	}
}

func TestNativeToolConcurrencyUsesOriginalEnvironmentWithoutChangingFiniteDefault(t *testing.T) {
	env := map[string]string{"CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY": "\ufeff2 suffix"}
	h := &Host{options: Options{OriginalEnv: env, Print: &PrintOptions{NativeProcess: true}}}
	if h.toolConcurrency() != 2 {
		t.Fatal("native orchestration ignored actual environment")
	}
	h.options.Print.NativeProcess = false
	if h.toolConcurrency() != 10 {
		t.Fatal("old finite policy changed")
	}
	request, err := NativePrintRequestEnvironment(env)
	if err != nil || len(request) != 0 || env["CLAUDE_CODE_MAX_TOOL_USE_CONCURRENCY"] != "\ufeff2 suffix" {
		t.Fatal("local orchestration control mutated or became provider request input")
	}
}

func TestNativePreflightEnvironmentOwnsWindowAndOutputControls(t *testing.T) {
	original := map[string]string{"CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"actual-model":16000}`, "CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS": "4000", "PATH": "/native/tools"}
	before := map[string]string{}
	for k, v := range original {
		before[k] = v
	}
	request, e := NativePrintRequestEnvironment(original)
	if e != nil {
		t.Fatal(e)
	}
	if _, ok := request["CLAUDE_CODE_MODEL_CONTEXT_WINDOWS"]; ok {
		t.Fatal("local budget became provider request capability")
	}
	if !nativePreflightEnvironment("CLAUDE_CODE_MODEL_CONTEXT_WINDOWS") || !nativePreflightEnvironment("CLAUDE_CODE_PROVIDER_MAX_OUTPUT_TOKENS") || nativePreflightEnvironment("PATH") {
		t.Fatal("native local policy projection changed")
	}
	if !reflect.DeepEqual(original, before) {
		t.Fatal("original OS environment mutated")
	}
}

func TestOriginalHistoryOwnerAndSettledLifecycleGates(t *testing.T) {
	owner, foreign := cooperative.New(), cooperative.New()
	h := &Host{options: Options{Owner: owner}, askRunning: true}
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := h.OriginalHistoryOnOwner(scope)
		if e != cooperative.ErrRestricted {
			t.Fatalf("foreign owner accepted: %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if e := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if _, e := h.OriginalHistoryOnOwner(scope); e == nil || !strings.Contains(e.Error(), "lifecycle") {
			t.Fatalf("active ASK history accepted: %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
