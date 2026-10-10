package automode

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
	"os"
	"testing"
)

func TestActualSourceOriginalClassifierSignedRequest(t *testing.T) {
	for _, profile := range []string{"actual-permission-allow", "actual-permission-block"} {
		t.Run(profile, func(t *testing.T) {
			history := []protocol.JSONObject{obj("type", quoted("user"))}
			m := obj("role", quoted("user"))
			m.Set("content", quoted("Use the private fixture workspace only"))
			history[0].Set("message", encoded(m))
			snapshot := Snapshot{TranscriptClassifier: true, Sources: map[string]json.RawMessage{"userSettings": []byte(`{"skipAutoPermissionPrompt":true,"autoMode":{"classifyAllShell":true}}`)}}
			options, e := ClassifierOptions(history, "Bash", []byte(`{"command":"printf safe"}`), []string{"EnterPlanMode", "ExitPlanMode", "Bash"}, snapshot, nil)
			if e != nil {
				t.Fatal(e)
			}
			context := requestcontext.Context{Options: requestcontext.Options{Model: "fixture-model", QuerySource: "auto_mode"}, Host: requestcontext.Host{Profile: requestcontext.NativeDirectRelayProfile, Env: map[string]string{"ANTHROPIC_BASE_URL": "http://127.0.0.1:4000", "ANTHROPIC_API_KEY": "offline-fixture-key", "CLAUDE_CODE_ENTRYPOINT": "cli"}, DeviceID: "original-fixture-device", SessionID: "11111111-1111-4111-8111-111111111111"}}
			params, e := requestcontext.NativeSideQueryRequest(context, protocol.NewJSONObject(), options)
			if e != nil {
				t.Fatal(e)
			}
			raw, e := params.MarshalJSON()
			if e != nil {
				t.Fatal(e)
			}
			value, e := requestmapping.ParseValue(raw)
			if e != nil {
				t.Fatal(e)
			}
			request, e := provider.PrepareBetaMessages(value, provider.BetaMessageOptions{})
			if e != nil {
				t.Fatal(e)
			}
			expected, e := os.ReadFile("testdata/" + profile + "-body.json")
			if e != nil {
				t.Fatal(e)
			}
			if !bytes.Equal(request.Body, expected) {
				t.Fatalf("actual signed Source differs\nSource=%s\nGo=%s", expected, request.Body)
			}
		})
	}
}
