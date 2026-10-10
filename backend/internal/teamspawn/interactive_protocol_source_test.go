package teamspawn

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestInteractiveInboxProtocolSchemasAgainstActualSource(t *testing.T) {
	probes := []string{
		`not json`, `null`, `{"type":"permission_request","unknown":true}`, `{"type":"sandbox_permission_request","extra":1}`,
		`{"type":"permission_response","request_id":"p","subtype":"success"}`, `{"type":"permission_response","request_id":"","subtype":"error","error":"no"}`,
		`{"type":"permission_response","request_id":"p","subtype":"error","error":"no","extra":1}`,
		`{"type":"permission_response","request_id":"p","subtype":"success","response":{"updated_input":{"future":true},"permission_updates":[{"type":"setMode","mode":"plan","destination":"session","extra":true}]}}`,
		`{"type":"permission_response","request_id":"p","subtype":"success","response":{"extra":true}}`,
		`{"type":"sandbox_permission_response","requestId":"s","host":"example.test","allow":false,"timestamp":"t"}`,
		`{"type":"sandbox_permission_response","requestId":"s","host":"example.test","allow":false,"timestamp":"t","extra":true}`,
		`{"type":"team_permission_update","directoryPath":"/fixture","toolName":"Write","permissionUpdate":{"type":"addRules","rules":[{"toolName":"Write","extra":1}],"behavior":"allow","destination":"session"}}`,
		`{"type":"team_permission_update","directoryPath":"/fixture","toolName":"Write","permissionUpdate":{"type":"addRules","rules":[],"behavior":"allow","destination":"session","extra":1}}`,
		`{"type":"shutdown_request","requestId":"","from":"worker","timestamp":"t","extra":1}`,
		`{"type":"shutdown_approved","requestId":"s","from":"worker","timestamp":"t","paneId":"%claimed","backendType":"tmux"}`,
		`{"type":"shutdown_approved","requestId":"s","from":"worker","timestamp":"t","extra":1}`,
		`{"type":"plan_approval_request","requestId":"p","from":"worker","timestamp":"t","planFilePath":"/fixture","planContent":"plan","extra":1}`,
		`{"type":"plan_approval_response","requestId":"p","approved":true,"timestamp":"t","permissionMode":"default","extra":1}`,
		`{"type":"mode_set_request","mode":"plan","from":"team-lead","extra":1}`, `{"type":"mode_set_request","mode":"bad","from":"team-lead"}`,
		`{"type":"mode_set_request","mode":"auto","from":"team-lead"}`,
		`{"type":"plan_approval_response","requestId":"p","approved":true,"timestamp":"t","permissionMode":"auto"}`,
	}
	actual := []map[string]any{}
	xml := []string{`plain`, "<teammate-message teammate_id=\"worker\" color=\"blue\" summary=\"actual\">\n first \n</teammate-message>\n\n<teammate-message teammate_id=\"other\">second</teammate-message>", `<teammate-message teammate_id="worker"> </teammate-message>`, `<teammate-message teammate_id="">ignored</teammate-message>`, `<teammate-message teammate_id="worker" summary="actual">😀</teammate-message>`}
	parsedXML := [][]protocol.JSONObject{}
	xml = append(xml, `<teammate-message\steammate_id="worker"\scolor="blue"\ssummary="literal Source pattern">\nSSsss\n<\/teammate-message>`)
	for _, text := range xml {
		parsedXML = append(parsedXML, ParseInteractiveTeammateXML(text))
	}
	names := map[string]string{"permission_request": "isPermissionRequest", "permission_response": "isPermissionResponse", "sandbox_permission_request": "isSandboxPermissionRequest", "sandbox_permission_response": "isSandboxPermissionResponse", "shutdown_request": "isShutdownRequest", "shutdown_approved": "isShutdownApproved", "team_permission_update": "isTeamPermissionUpdate", "mode_set_request": "isModeSetRequest", "plan_approval_request": "isPlanApprovalRequest", "plan_approval_response": "isPlanApprovalResponse"}
	for _, text := range probes {
		kind, p := ParseInteractiveProtocol(text)
		values := []map[string]any{}
		if kind != "" {
			values = append(values, map[string]any{"name": names[kind], "value": p})
		}
		actual = append(actual, map[string]any{"values": values, "structured": StructuredProtocolMessage(text)})
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home, _ := filepath.EvalSymlinks(t.TempDir())
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-interactive-inbox.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_INTERACTIVE_INBOX_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": home, "parsers": probes, "xml": xml}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String(), string(out))
	}
	var source struct {
		Parsed, XML  json.RawMessage
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatal(err, string(out), stderr.String())
	}
	var a, b any
	_ = json.Unmarshal(rawJSON(actual), &a)
	_ = json.Unmarshal(source.Parsed, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("Go%s\nSource%s", rawJSON(actual), source.Parsed)
	}
	_ = json.Unmarshal(rawJSON(parsedXML), &a)
	_ = json.Unmarshal(source.XML, &b)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("GoXML%s\nSourceXML%s", rawJSON(parsedXML), source.XML)
	}
}
