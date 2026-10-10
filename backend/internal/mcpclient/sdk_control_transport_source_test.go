package mcpclient

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSDKControlTransportActualOriginalSetupAndPhysicalPeer(t *testing.T) {
	home, e := filepath.EvalSymlinks(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	if e = os.MkdirAll(filepath.Join(home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{}`), 0600); e != nil {
		t.Fatal(e)
	}
	fixture, _ := filepath.Abs("../../testdata/native-sdk-mcp-transport.fixture.ts")
	preload, _ := filepath.Abs("../../../preload")
	sandbox, _ := filepath.Abs("../../testdata/mcp-loopback-only.sb")
	ctx, finish := context.WithTimeout(context.Background(), 20*time.Second)
	defer finish()
	makeCommand := func(peer bool) *exec.Cmd {
		c := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", sandbox, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", "--preload", preload, fixture)
		c.Dir = home
		c.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_SDK_MCP_TRANSPORT_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
		if peer {
			c.Env = append(c.Env, "CC_HAHA_SDK_MCP_PEER=1")
		}
		return c
	}
	original := makeCommand(false)
	var originalErrors bytes.Buffer
	original.Stderr = &originalErrors
	rawSource, e := original.Output()
	if e != nil {
		t.Fatal("actual Source SDK setup/transport", e, originalErrors.String())
	}
	var source struct {
		Messages []struct {
			Name    string
			Message protocol.JSONObject
		}
		Tools      []string
		Deliveries []struct {
			Name     string
			Message  protocol.JSONObject
			Received int
		}
		Notifications []struct {
			Method string
			Params protocol.JSONObject
		}
		Call         protocol.JSONObject
		File         string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(rawSource, &source); e != nil {
		t.Fatal(e, string(rawSource))
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source escaped capabilities", source.Capabilities.Blocked)
	}
	if !reflect.DeepEqual(source.Tools, []string{"mcp__peer__touch"}) || source.File != "ACTUAL_SDK_PHYSICAL" {
		t.Fatal("actual Source SDK consumer", string(rawSource))
	}
	_ = os.Remove(filepath.Join(home, "sdk-physical.txt"))
	peer := makeCommand(true)
	var peerErrors bytes.Buffer
	peer.Stderr = &peerErrors
	stdin, e := peer.StdinPipe()
	if e != nil {
		t.Fatal(e)
	}
	stdout, e := peer.StdoutPipe()
	if e != nil {
		t.Fatal(e)
	}
	if e = peer.Start(); e != nil {
		t.Fatal(e)
	}
	scanner := bufio.NewScanner(stdout)
	var mu sync.Mutex
	var packets []protocol.JSONObject
	bridge := &SDKControlBindings{Send: func(_ context.Context, packet protocol.JSONObject) (json.RawMessage, error) {
		mu.Lock()
		defer mu.Unlock()
		packets = append(packets, packet.Clone())
		data, e := protocol.EncodeNDJSON(packet)
		if e != nil {
			return nil, e
		}
		if _, e = stdin.Write(data); e != nil {
			return nil, e
		}
		if !scanner.Scan() {
			if e = scanner.Err(); e == nil {
				e = context.Canceled
			}
			return nil, e
		}
		return append(json.RawMessage(nil), scanner.Bytes()...), nil
	}}
	var notifications []struct {
		Method string
		Params protocol.JSONObject
	}
	client, e := Connect(ctx, Config{Type: "sdk"}, Options{Name: "peer", Version: "fixture", CWD: home, Environment: map[string]string{}, Capabilities: protocol.NewJSONObject(), SDK: bridge, ConnectTimeout: 3 * time.Second, OnNotification: func(method string, params protocol.JSONObject) {
		if method == "notifications/tools/list_changed" {
			notifications = append(notifications, struct {
				Method string
				Params protocol.JSONObject
			}{method, params.Clone()})
		}
	}})
	if e != nil {
		t.Fatal("real Go SDK transport", e, peerErrors.String())
	}
	defer client.Close()
	if _, e = client.ListTools(ctx, nil); e != nil {
		t.Fatal(e)
	}
	call, e := client.CallTool(ctx, "touch", object(field("text", "ACTUAL_SDK_PHYSICAL")), protocol.JSONObject{}, RequestOptions{})
	if e != nil {
		t.Fatal(e)
	}
	for _, delivery := range source.Deliveries {
		if delivery.Name == "peer" {
			if e = client.DeliverSDKMessage(delivery.Message); e != nil {
				t.Fatal(e)
			}
		}
		if len(notifications) != delivery.Received {
			t.Fatal("actual Source notification routing", delivery.Name, len(notifications), delivery.Received)
		}
	}
	if len(notifications) != 1 || len(source.Notifications) != 1 || notifications[0].Method != source.Notifications[0].Method || !sameSDKJSONObject(notifications[0].Params, source.Notifications[0].Params) {
		t.Fatal("actual Source notification callback", notifications, source.Notifications)
	}
	if e = client.Close(); e != nil {
		t.Fatal(e)
	}
	_ = stdin.Close()
	if e = peer.Wait(); e != nil {
		t.Fatal("actual peer Close", e, peerErrors.String())
	}
	physical, e := os.ReadFile(filepath.Join(home, "sdk-physical.txt"))
	if e != nil || string(physical) != source.File {
		t.Fatal("Go did not consume same actual SDK server", string(physical), e)
	}
	if !sameSDKJSONObject(call, source.Call) {
		t.Fatal("actual Source tools/call result", string(raw(call)), string(raw(source.Call)))
	}
	mu.Lock()
	defer mu.Unlock()
	if len(packets) != len(source.Messages) {
		t.Fatal("Source SDK message count", len(packets), len(source.Messages))
	}
	for i, packet := range packets {
		if !sameSDKJSONObject(packet, source.Messages[i].Message) {
			t.Fatalf("actual SDK wire %d\nGo %s\nSource %s", i, raw(packet), raw(source.Messages[i].Message))
		}
	}
}
func sameSDKJSONObject(a, b protocol.JSONObject) bool {
	var x, y any
	_ = json.Unmarshal(raw(a), &x)
	_ = json.Unmarshal(raw(b), &y)
	return reflect.DeepEqual(x, y)
}
