package application

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"golang.org/x/net/websocket"
)

func TestActualDesktopMarketInstallRefreshesWarmSameSessionSkillSlashWithoutRestart(t *testing.T) {
	const content = "---\nname: warm-market-skill\ndescription: Actual warm desktop installation\n---\nWARM_INSTALLED_INSTRUCTIONS $ARGUMENTS\n"
	market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/file"):
			fmt.Fprint(w, content)
		case strings.Contains(r.URL.Path, "/versions/"):
			json.NewEncoder(w).Encode(map[string]any{"version": map[string]any{"version": "1.0", "files": []any{map[string]any{"path": "SKILL.md", "size": len(content)}}}})
		case strings.HasSuffix(r.URL.Path, "/warm-market-skill"):
			json.NewEncoder(w).Encode(map[string]any{"skill": map[string]any{"slug": "warm-market-skill", "displayName": "Warm Market Skill", "description": "Actual warm installation"}, "latestVersion": map[string]any{"version": "1.0"}})
		default:
			t.Errorf("unexpected marketplace request %s", r.URL.Path)
			http.Error(w, "missing", 404)
		}
	}))
	defer market.Close()
	var posts atomic.Int32
	requests := make(chan []byte, 4)
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		response(w, fmt.Sprintf("warm-%d", posts.Add(1)), "", nil, "ACTUAL WARM TURN COMPLETE")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Environment["HAHA_MARKET_BASE_CLAWHUB"] = market.URL
	o.Environment["HAHA_MARKET_BASE_SKILLHUB"] = market.URL
	var nativeFactories atomic.Int32
	s, err := NewDesktopServer(ServerOptions{Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: o.Environment["CLAUDE_CONFIG_DIR"], WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, os.ErrPermission
	}, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		nativeFactories.Add(1)
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	host := httptest.NewServer(s)
	defer host.Close()
	call := func(path, body string, status int) protocol.JSONObject {
		t.Helper()
		res, e := host.Client().Post(host.URL+path, "application/json", strings.NewReader(body))
		if e != nil {
			t.Fatal(e)
		}
		defer res.Body.Close()
		data, e := io.ReadAll(res.Body)
		if e != nil || res.StatusCode != status {
			t.Fatalf("%s: %d %s %v", path, res.StatusCode, data, e)
		}
		return obj(data)
	}
	id := call("/api/sessions", string(raw(map[string]any{"workDir": o.WorkDir})), 201).String("sessionId")
	socket, err := websocket.Dial("ws"+strings.TrimPrefix(host.URL, "http")+"/ws/"+id, "", host.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer socket.Close()
	_ = socket.SetDeadline(time.Now().Add(20 * time.Second))
	sawInstalledMenu := false
	ask := func(prompt string) []byte {
		t.Helper()
		if e := websocket.Message.Send(socket, string(raw(map[string]string{"type": "user_message", "content": prompt}))); e != nil {
			t.Fatal(e)
		}
		for {
			var data string
			if e := websocket.Message.Receive(socket, &data); e != nil {
				t.Fatal(e)
			}
			frame := obj([]byte(data))
			if frame.String("type") == "system_notification" && frame.String("subtype") == "slash_commands" && strings.Contains(string(frame.Get("data")), `"name":"warm-market-skill"`) {
				sawInstalledMenu = true
			}
			if frame.String("type") == "error" {
				t.Fatal("actual same-session ASK failed", data)
			}
			if frame.String("type") == "message_complete" {
				select {
				case body := <-requests:
					return body
				default:
					t.Fatal("completed ASK did not reach actual model transport")
				}
			}
		}
	}
	ask("Warm this exact conversation before any skill directory exists.")
	beforeFactories := nativeFactories.Load()
	call("/api/market/install", `{"id":"clawhub:warm-market-skill"}`, 200)
	second := ask("/warm-market-skill same-session-argument")
	for _, want := range []string{"WARM_INSTALLED_INSTRUCTIONS same-session-argument", "<command-name>/warm-market-skill</command-name>", "Warm this exact conversation before any skill directory exists."} {
		if !strings.Contains(string(second), want) {
			t.Fatalf("warm native skill/history missing %q from actual request %s", want, second)
		}
	}
	if posts.Load() != 2 {
		t.Fatal("installation started an extra model turn", posts.Load())
	}
	if !sawInstalledMenu || nativeFactories.Load() != beforeFactories {
		t.Fatal("installation did not update live slash menu or replaced the native runtime", sawInstalledMenu, beforeFactories, nativeFactories.Load())
	}
	call("/api/market/uninstall", `{"id":"clawhub:warm-market-skill"}`, 200)
	if e := websocket.Message.Send(socket, `{"type":"user_message","content":"/warm-market-skill removed-command"}`); e != nil {
		t.Fatal(e)
	}
	sawRemovedMenu := false
	for {
		var data string
		if e := websocket.Message.Receive(socket, &data); e != nil {
			t.Fatal(e)
		}
		frame := obj([]byte(data))
		if frame.String("type") == "system_notification" && frame.String("subtype") == "slash_commands" && !strings.Contains(string(frame.Get("data")), `"name":"warm-market-skill"`) {
			sawRemovedMenu = true
		}
		if frame.String("type") == "error" {
			if !strings.Contains(frame.String("message"), "unknown actual prompt command warm-market-skill") {
				t.Fatal("uninstall caused unrelated failure", data)
			}
			break
		}
	}
	if !sawRemovedMenu || posts.Load() != 2 {
		t.Fatal("removed skill stayed executable or slash menu stale", sawRemovedMenu, posts.Load())
	}
	ask("Continue this same conversation after removing the temporary skill.")
	if posts.Load() != 3 || nativeFactories.Load() != beforeFactories {
		t.Fatal("uninstall broke same-runtime ordinary continuation", posts.Load(), nativeFactories.Load())
	}
}
