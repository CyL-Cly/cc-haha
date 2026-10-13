package querypreflight

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

func TestNativeRuntimeModelSamplesLivePermissionUsageBeforeSignaturesAndKeepsMainBudget(t *testing.T) {
	service, _, _, _, _ := testServices(t)
	in := base(t)
	in.Profile = NativeProcessProfile
	in.MainLoopModel = "actual-main"
	in.OriginalEnvironment = map[string]string{"DISABLE_AUTO_COMPACT": "1", "CLAUDE_CODE_MODEL_CONTEXT_WINDOWS": `{"actual-main":1000000,"actual-other":16000}`}
	live := &streamhistory.Envelope{Record: record(t, `{"type":"assistant","uuid":"live"}`), Message: record(t, `{"model":"actual-main","id":"original","content":[]}`), Content: []*streamhistory.Block{{Object: record(t, `{"type":"thinking","thinking":"secret","signature":"original-signature"}`)}, {Object: record(t, `{"type":"text","text":"original text"}`)}}, Usage: &streamhistory.Usage{Raw: json.RawMessage(`{"input_tokens":200000,"output_tokens":1}`)}}
	in.History = []queryloop.Message{{Assistant: live}, in.History[0]}
	before, _ := live.MarshalJSON()
	steps := []string{}
	in.CurrentPermissionMode = func() (string, error) { steps = append(steps, "permission"); return "plan", nil }
	var samples []RuntimeModelInput
	in.ResolveRuntimeModel = func(actual RuntimeModelInput) (string, error) {
		steps = append(steps, "model")
		samples = append(samples, actual)
		return "actual-other", nil
	}
	out, err := service.Run(context.Background(), in)
	if err != nil || out.Blocked != nil || out.CurrentModel != "actual-other" || out.Statistics.BlockingEstimate < 200001 {
		t.Fatalf("runtime model changed configured capacity or failed: %+v %v", out, err)
	}
	if !reflect.DeepEqual(steps, []string{"permission", "model"}) || len(samples) != 1 || samples[0] != (RuntimeModelInput{MainLoopModel: "actual-main", PermissionMode: "plan", Exceeds200kTokens: true}) {
		t.Fatalf("runtime observation did not use live mode/usage: %+v %+v", steps, samples)
	}
	if out.ToolContextHistory[0].Assistant != live || out.ModelHistory[0].Assistant != nil || strings.Contains(stringsOf(t, out.ModelHistory)[0], "original-signature") {
		t.Fatal("model projection ran before selection or replaced original tool context")
	}
	after, _ := live.MarshalJSON()
	if string(before) != string(after) {
		t.Fatal("runtime projection mutated live history")
	}
	// The following model iteration sees later Source stream usage on the same
	// live envelope, rather than a frozen SDK result or driver accounting total.
	live.Usage.Raw = json.RawMessage(`{"input_tokens":0,"output_tokens":0}`)
	out, err = service.Run(context.Background(), in)
	if err != nil || out.CurrentModel != "actual-other" || len(samples) != 2 || samples[1].Exceeds200kTokens {
		t.Fatalf("later live usage was frozen: %+v %v", samples, err)
	}
}

func TestNativeRuntimeModelRunsAfterBoundaryAndAutocompactBeforeBlocking(t *testing.T) {
	service, _, _, _, _ := testServices(t)
	in := base(t)
	in.Profile = NativeProcessProfile
	in.OriginalEnvironment["DISABLE_AUTO_COMPACT"] = "1"
	old := queryloop.Message{Record: record(t, `{"type":"assistant","message":{"model":"deepseek-flash","content":[],"usage":{"input_tokens":200000,"output_tokens":1}}}`)}
	boundary := queryloop.Message{Record: record(t, `{"type":"system","subtype":"compact_boundary","compactMetadata":{"trigger":"manual","preTokens":200001}}`)}
	in.History = []queryloop.Message{old, boundary, in.History[0]}
	in.PermissionMode = "plan"
	called := 0
	in.ResolveRuntimeModel = func(actual RuntimeModelInput) (string, error) {
		called++
		if actual.Exceeds200kTokens {
			t.Fatal("old precompact usage reached runtime selection")
		}
		return "deepseek-flash", nil
	}
	out, err := service.Run(context.Background(), in)
	if err != nil || called != 1 || out.Statistics.BoundaryIndex != 1 {
		t.Fatalf("compact boundary ordering: %+v %v", out, err)
	}
	// Active autocompact must fail before setup. Missing implementation cannot
	// invoke a successful runtime callback and continue with stale history.
	in.History = []queryloop.Message{old}
	delete(in.OriginalEnvironment, "DISABLE_AUTO_COMPACT")
	in.OriginalEnvironment["CLAUDE_CODE_AUTO_COMPACT_WINDOW"] = "16"
	_, err = service.Run(context.Background(), in)
	if err == nil || !strings.Contains(err.Error(), "active autocompact") || called != 1 {
		t.Fatalf("runtime callback bypassed active compact: calls=%d %v", called, err)
	}
	// A failed model resolver exits before creating a blocking error message.
	in.OriginalEnvironment["DISABLE_COMPACT"] = "1"
	in.OriginalEnvironment["CLAUDE_CODE_BLOCKING_LIMIT_OVERRIDE"] = "1"
	failure := errors.New("original model authority failure")
	in.ResolveRuntimeModel = func(RuntimeModelInput) (string, error) { return "", failure }
	out, err = service.Run(context.Background(), in)
	if !errors.Is(err, failure) || out.Blocked != nil {
		t.Fatalf("model authority failure was swallowed: %+v %v", out, err)
	}
}

func TestRuntimeCallbacksKeepFiniteAndMissingAuthorityGuards(t *testing.T) {
	service, _, _, _, _ := testServices(t)
	in := base(t)
	in.ResolveRuntimeModel = func(RuntimeModelInput) (string, error) { t.Fatal("finite callback invoked"); return "", nil }
	_, err := service.Run(context.Background(), in)
	requireCap(t, err)
	in.Profile = NativeProcessProfile
	in.ResolveRuntimeModel = nil
	in.CurrentPermissionMode = func() (string, error) { t.Fatal("permission without model authority invoked"); return "", nil }
	_, err = service.Run(context.Background(), in)
	requireCap(t, err)
}

func TestNativeDontAskRequiresActualRuntimeAuthorityAndPassesModeUnchanged(t *testing.T) {
	for _, mode := range []string{"dontAsk", "auto"} {
		service, _, _, _, _ := testServices(t)
		in := base(t)
		in.Profile = NativeProcessProfile
		in.PermissionMode = mode
		in.QuerySource = "hook_agent"
		_, err := service.Run(context.Background(), in)
		requireCap(t, err)
		in.ResolveRuntimeModel = func(actual RuntimeModelInput) (string, error) {
			if actual.PermissionMode != mode || actual.MainLoopModel != in.MainLoopModel {
				return "", errors.New("temporary hook mode/model lost")
			}
			return actual.MainLoopModel, nil
		}
		out, err := service.Run(context.Background(), in)
		if err != nil || out.CurrentModel != in.MainLoopModel {
			t.Fatal(out, err)
		}
	}
}

func TestNativeMCPPreflightOnlyOriginalWorkflowChildEntrypoint(t *testing.T) {
	in := base(t)
	in.Profile = NativeProcessProfile
	in.OriginalEnvironment["CLAUDE_CODE_ENTRYPOINT"] = "mcp"
	for _, source := range []string{"sdk", "repl_main_thread", "workflow_agent"} {
		in.QuerySource = source
		err := validate(in)
		if source == "workflow_agent" && err != nil {
			t.Fatal(err)
		}
		if source != "workflow_agent" && err == nil {
			t.Fatal("MCP main loop admitted", source)
		}
	}
	in.Profile = ""
	if validate(in) == nil {
		t.Fatal("finite MCP profile admitted")
	}
}
