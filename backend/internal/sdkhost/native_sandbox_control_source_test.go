package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSyntheticSandboxSDKPendingAnswerEOFSchemaAndReplay(t *testing.T) {
	rows := []map[string]any{}
	for _, response := range []any{map[string]any{"behavior": "allow", "updatedInput": map[string]any{}}, map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "updatedPermissions": true, "decisionClassification": 9}, map[string]any{"behavior": "allow"}, map[string]any{"behavior": "allow", "updatedInput": nil}, map[string]any{"behavior": "allow", "updatedInput": []string{}}, map[string]any{"behavior": "allow", "updatedInput": map[string]any{}, "toolUseID": 1}, map[string]any{"behavior": "deny", "message": "declined"}, map[string]any{"behavior": "deny"}, map[string]any{"behavior": 1}, nil} {
		for _, replay := range []bool{false, true} {
			rows = append(rows, map[string]any{"host": "pending.owned.test", "response": response, "replay": replay})
		}
	}
	rows = append(rows, map[string]any{"host": "closed.owned.test", "close": true}, map[string]any{"host": "error.owned.test", "error": true, "replay": true})
	input, _ := json.Marshal(map[string]any{"rows": rows})
	fixture, _ := filepath.Abs("testdata/sandbox-control.fixture.ts")
	root, _ := filepath.Abs("../../..")
	home := t.TempDir()
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", filepath.Join(root, "preload.ts"), fixture)
	cmd.Env = []string{"HOME=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=/usr/bin:/bin", "NODE_ENV=test", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(input)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []json.RawMessage
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, row := range rows {
		owner := cooperative.New()
		frames := []protocol.JSONObject{}
		host := &Host{options: Options{Owner: owner, UUID: func() string { return "<request>" }, OnPhase: func(string) {}, Output: Output{Enqueue: func(data []byte) error {
			var frame protocol.JSONObject
			frame.UnmarshalJSON(data)
			request, _ := frame.Object("request")
			request.Set("tool_use_id", quote("<tool>"))
			frame.Set("request", raw(request))
			frames = append(frames, frame)
			return nil
		}}}, sandboxInputOpen: true}
		observation := protocol.NewJSONObject()
		e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			future, e := host.startSDKNativeSandboxPermission(context.Background(), scope, row["host"].(string))
			if e != nil {
				return e
			}
			observation.Set("frame", raw(frames[0]))
			observation.Set("waiting", raw(len(host.sandboxPending)))
			observation.Set("disconnected", raw(len(host.sandboxPending)))
			var replay json.RawMessage = raw(nil)
			if row["close"] == true {
				if e = host.closeNativeSandboxInput(scope); e != nil {
					return e
				}
			} else {
				response := protocol.NewJSONObject(protocol.JSONField{Name: "subtype", Value: quote("success")}, protocol.JSONField{Name: "request_id", Value: quote("<request>")}, protocol.JSONField{Name: "response", Value: raw(row["response"])})
				if row["error"] == true {
					response.Delete("response")
					response.Set("subtype", quote("error"))
					response.Set("error", quote("owned SDK host rejected"))
				}
				message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("control_response")}, protocol.JSONField{Name: "response", Value: raw(response)})
				handled, e := host.nativeSandboxControlResponse(scope, message)
				if e != nil || !handled {
					return e
				}
				if row["replay"] == true && row["error"] != true {
					replay = raw(message)
				}
			}
			allowed, e := future.Await(scope)
			if e != nil {
				return e
			}
			observation.Set("allowed", raw(allowed))
			observation.Set("remaining", raw(len(host.sandboxPending)))
			observation.Set("replay", replay)
			return nil
		})
		owner.Close()
		if e != nil {
			t.Fatal(i, e)
		}
		var expected, actual any
		json.Unmarshal(source[i], &expected)
		json.Unmarshal(raw(observation), &actual)
		if !reflect.DeepEqual(expected, actual) {
			t.Fatalf("Source%d row%v\nSource%s\nGo%s", i, row, source[i], raw(observation))
		}
	}
}
