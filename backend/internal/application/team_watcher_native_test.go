package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/net/websocket"
)

// No model transport is used: original CLI-owned Team files must themselves
// drive the actual desktop socket. An HTTP workbench alone cannot enable UI.
func TestDesktopServerActualTeamFilesEmitLeadLifecycleWithoutModel(t *testing.T) {
	o := nativeSessionOptions(t, "http://127.0.0.1:1")
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return newIsolatedProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	socket, err := websocket.Dial(strings.Replace(host.URL, "http:", "ws:", 1)+"/ws/watcher-parent", "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	if err = socket.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(config, "teams", "native-watch")
	if err = os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"name":"native-watch","createdAt":1000,"leadSessionId":"watcher-parent","leadAgentId":"team-lead@native-watch","members":[{"agentId":"team-lead@native-watch","name":"team-lead","isActive":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	for {
		var text string
		if err = websocket.Message.Receive(socket, &text); err != nil {
			t.Fatalf("actual CLI-owned team never reached desktop UI: %v", err)
		}
		var event struct {
			Type, TeamName, LeadSessionID, IncarnationID string
			CreatedAt                                    float64
		}
		if json.Unmarshal([]byte(text), &event) == nil && event.Type == "team_created" {
			if event.TeamName != "native-watch" || event.LeadSessionID != "watcher-parent" || event.CreatedAt != 1000 || event.IncarnationID == "" {
				t.Fatal(text)
			}
			break
		}
	}
	response, err := host.Client().Get(host.URL + "/api/teams/native-watch/workbench")
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || response.StatusCode != http.StatusOK || !strings.Contains(string(data), `"name":"native-watch"`) {
		t.Fatal("event consumer cannot load actual workbench", err, response.StatusCode, string(data))
	}
	if err = os.WriteFile(filepath.Join(dir, "config.json"), []byte(`{"name":"native-watch","createdAt":1000,"leadSessionId":"watcher-parent","leadAgentId":"team-lead@native-watch","members":[{"agentId":"team-lead@native-watch","name":"team-lead","isActive":false},{"agentId":"reader@native-watch","name":"reader","isActive":false}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	if err = socket.SetDeadline(time.Now().Add(8 * time.Second)); err != nil {
		t.Fatal(err)
	}
	for !seen["team_update"] || !seen["team_workbench_updated"] {
		var text string
		if err = websocket.Message.Receive(socket, &text); err != nil {
			t.Fatal("joined workbench invalidation missing", err)
		}
		var event struct{ Type, TeamName string }
		if json.Unmarshal([]byte(text), &event) == nil && event.TeamName == "native-watch" {
			seen[event.Type] = true
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err = s.Close(ctx); err != nil {
		t.Fatal("server did not join watcher", err)
	}
}
