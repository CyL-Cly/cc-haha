package server

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Source keeps SDK activity and registered user admission in separate domains.
// Run its original observers, predicate, reconnect permission producer, and
// actual sync_state handler rather than inferring activity from a Go fixture.
func TestActualSourceForegroundSnapshotsIgnoreSDKOnlyRunAndIncludePendingStartup(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-desktop-completion-frames.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	command := exec.Command(bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_COMPLETION_FRAMES_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	homeJSON, err := json.Marshal(home)
	if err != nil {
		t.Fatal(err)
	}
	command.Stdin = bytes.NewBufferString(`{"home":` + string(homeJSON) + `,"sessionId":"private-sdk-only","pending":[{"requestId":"private-worker-write","toolName":"Write","toolUseId":"private-write","input":{"content":"retained"},"displayName":"worker"}],"result":{"type":"result","subtype":"success","is_error":false,"usage":{"input_tokens":1,"output_tokens":1}}}`)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("original snapshot observer failed", err, string(output))
	}
	var source struct {
		Connected                  []json.RawMessage
		SDKRunState                string
		SDKCliRunning              bool
		SDKOnlySnapshot            []json.RawMessage
		SDKOnlyPermissionsSnapshot []json.RawMessage
		PendingStartupSnapshot     []json.RawMessage
		StoppedPendingAdmission    struct {
			Cancelled, ReplacementAfterStop, StillRegistered, StillQueued bool
		}
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Capabilities.Blocked) != 0 {
		t.Fatal("invalid actual Source evidence", err, string(output))
	}
	if len(source.Connected) != 3 || stringField(objectForTest(source.Connected[0]), "type") != "connected" || stringField(objectForTest(source.Connected[1]), "type") != "permission_request" || stringField(objectForTest(source.Connected[2]), "type") != "permission_requests_snapshot" {
		t.Fatal("Source connect emitted unsolicited turn state or lost permission replay", string(output))
	}
	if source.SDKRunState != "running" || !source.SDKCliRunning || len(source.SDKOnlySnapshot) != 1 || stringField(objectForTest(source.SDKOnlySnapshot[0]), "turnState") != "idle" {
		t.Fatal("Source SDK-only activity replaced the foreground domain", string(output))
	}
	if len(source.SDKOnlyPermissionsSnapshot) != 2 {
		t.Fatal("Source SDK reconnect dropped pending worker request", string(output))
	}
	request, snapshot := objectForTest(source.SDKOnlyPermissionsSnapshot[0]), objectForTest(source.SDKOnlyPermissionsSnapshot[1])
	if stringField(request, "type") != "permission_request" || stringField(request, "requestId") != "private-worker-write" || stringField(snapshot, "type") != "permission_requests_snapshot" || string(snapshot["turnActive"]) != "false" || string(snapshot["toolRequestIds"]) != `["private-worker-write"]` {
		t.Fatal("Source reconnect did not preserve idle SDK-only permission", string(output))
	}
	if len(source.PendingStartupSnapshot) != 1 || stringField(objectForTest(source.PendingStartupSnapshot[0]), "turnState") != "running" {
		t.Fatal("Source pre-send user registration was not active", string(output))
	}
	if !source.StoppedPendingAdmission.Cancelled || source.StoppedPendingAdmission.ReplacementAfterStop || source.StoppedPendingAdmission.StillRegistered || source.StoppedPendingAdmission.StillQueued {
		t.Fatal("Source Stop failed to fence its unsent user and clear the old queue", string(output))
	}
}
