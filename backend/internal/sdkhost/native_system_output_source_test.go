package sdkhost

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestNativeRetryAndRecoveryActualQueryEngineOutputAndNextASKPersistence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	inputs := []protocol.JSONObject{}
	sid := "00000000-0000-4000-8000-000000000000"
	outputs := []protocol.JSONObject{}
	for i, status := range []int{0, 400, 401, 403, 408, 429, 500, 529} {
		failure := protocol.NewJSONObject(protocol.JSONField{Name: "message", Value: quote("ordinary SDK detail")})
		var statusPtr *int
		if status != 0 {
			statusPtr = &status
			failure.Set("status", raw(status))
		}
		original := &provider.SDKStreamError{Status: statusPtr, Message: failure.String("message")}
		if status == 400 {
			original.Message = `400 {"type":"overloaded_error"}`
			failure.Set("message", quote(original.Message))
		}
		message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("api_error")}, protocol.JSONField{Name: "level", Value: quote("error")}, protocol.JSONField{Name: "error", Value: raw(failure)}, protocol.JSONField{Name: "retryAttempt", Value: raw(1)}, protocol.JSONField{Name: "maxRetries", Value: raw(2)}, protocol.JSONField{Name: "retryInMs", Value: raw(500)}, protocol.JSONField{Name: "uuid", Value: quote("original-retry")}, protocol.JSONField{Name: "timestamp", Value: quote("2026-10-08T00:00:00.000Z")})
		message.Set("uuid", quote(fmt.Sprintf("original-retry-%d", i)))
		inputs = append(inputs, message)
		outputs = append(outputs, nativeAPIRetryOutput(message, sid, original))
	}
	for _, cause := range []string{"stream_retry", "watchdog", "stream_error", "404_stream_creation"} {
		message := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("system")}, protocol.JSONField{Name: "subtype", Value: quote("streaming_fallback")}, protocol.JSONField{Name: "level", Value: quote("info")}, protocol.JSONField{Name: "content", Value: quote("original recovery text")}, protocol.JSONField{Name: "cause", Value: quote(cause)}, protocol.JSONField{Name: "uuid", Value: quote("original-recovery-" + cause)}, protocol.JSONField{Name: "timestamp", Value: quote("2026-10-08T00:00:00.000Z")})
		inputs = append(inputs, message)
		outputs = append(outputs, nativeStreamRecoveryOutput(message, sid))
	}
	profiles := []map[string]any{{"sessionId": sid, "messages": inputs}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-sdk-system-output.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_SDK_SYSTEM_OUTPUT_FIXTURE=1", "NODE_ENV=production", "USER_TYPE=external", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_SIMPLE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "profiles": profiles})
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("actual QueryEngine %v %s", err, stderr.String())
	}
	var source struct {
		Rows         []struct{ Output, History, Persisted []map[string]any }
		Capabilities struct{ Blocked []string }
	}
	if err = json.Unmarshal(out, &source); err != nil || len(source.Rows) != 1 || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	row := source.Rows[0]
	if len(row.Output) != len(outputs) || len(row.History) != len(outputs) || len(row.Persisted) != len(outputs) {
		t.Fatalf("original system history/record count: %v", row)
	}
	for i := range outputs {
		encoded, _ := outputs[i].MarshalJSON()
		var got map[string]any
		_ = json.Unmarshal(encoded, &got)
		if !reflect.DeepEqual(row.Output[i], got) || row.History[i]["subtype"] != inputs[i].String("subtype") || row.Persisted[i]["subtype"] != inputs[i].String("subtype") {
			t.Fatalf("actual system dispatch/history%d got%s Source%v", i, encoded, row)
		}
	}
}
