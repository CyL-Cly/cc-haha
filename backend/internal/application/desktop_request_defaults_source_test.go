package application

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/server"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

type defaultTaskSourceRow struct {
	Enabled      bool
	Create, List string
}

func actualDefaultTaskSource(t *testing.T, rows []map[string]any) (out struct {
	Rows []defaultTaskSourceRow
	Mode []string
}) {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual original Source Bun required")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../testdata/native-default-task-prompts.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	child.Dir = home
	child.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "NODE_ENV=production", "CC_HAHA_DEFAULT_TASK_PROMPTS_FIXTURE=1", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "PATH=/usr/bin:/bin"}
	child.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "rows": rows}))
	var stderr bytes.Buffer
	child.Stderr = &stderr
	data, err := child.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	if err = json.Unmarshal(data, &out); err != nil {
		t.Fatal(err, string(data))
	}
	return
}

func TestActualDesktopDefaultRequestUsesOriginalTeamPromptsModeOrderAndTimeout(t *testing.T) {
	source := actualDefaultTaskSource(t, []map[string]any{{"env": map[string]string{"USER_TYPE": "external", "CC_HAHA_AGENT_TEAMS_ENABLED": "1"}}})
	var mu sync.Mutex
	var requests []json.RawMessage
	var timeouts []string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		requests = append(requests, body)
		timeouts = append(timeouts, r.Header.Get("X-Stainless-Timeout"))
		mu.Unlock()
		response(w, "defaults", "", nil, "actual request admitted")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = nil
	o.Arguments.SystemPrompt = ""
	o.Arguments.PermissionMode = "default"
	o.Arguments.Model = "deepseek-flash[1m]"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	settings := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settings, []byte(`{"agentTeamsEnabled":true,"network":{"proxy":{"mode":"direct"}},"future":{"keep":true}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	defer live.Close(context.Background())
	if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Inspect the actual default request without tools."})); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(requests) != 1 {
		t.Fatalf("actual request count %d", len(requests))
	}
	if timeouts[0] != "1800" {
		t.Errorf("actual Desktop SDK timeout Source1800 Go%s", timeouts[0])
	}
	var wire struct {
		Tools []struct {
			Name, Description string
			InputSchema       struct {
				Properties map[string]struct{ Enum []string }
			} `json:"input_schema"`
		}
	}
	if err = json.Unmarshal(requests[0], &wire); err != nil {
		t.Fatal(err)
	}
	found := map[string]bool{}
	for _, tool := range wire.Tools {
		switch tool.Name {
		case "Agent":
			found[tool.Name] = true
			if !reflect.DeepEqual(tool.InputSchema.Properties["mode"].Enum, source.Mode) {
				t.Errorf("actual Agent.mode Source%v Go%v", source.Mode, tool.InputSchema.Properties["mode"].Enum)
			}
		case "TaskCreate":
			found[tool.Name] = true
			if tool.Description != source.Rows[0].Create {
				t.Error("actual TaskCreate omitted original active-Team description")
			}
		case "TaskList":
			found[tool.Name] = true
			if tool.Description != source.Rows[0].List {
				t.Error("actual TaskList omitted original active-Team description")
			}
		}
	}
	if len(found) != 3 {
		t.Fatal("ordinary tool catalog incomplete", found)
	}
}

func TestActualDesktopNetworkRefreshChangesNextRequestAndPreservesStandaloneDefault(t *testing.T) {
	for _, desktop := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary-cli-600", true: "desktop-refresh"}[desktop], func(t *testing.T) {
			var mu sync.Mutex
			var timeout []string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				mu.Lock()
				timeout = append(timeout, r.Header.Get("X-Stainless-Timeout"))
				mu.Unlock()
				response(w, "network-refresh", "", nil, "actual next request")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Arguments.PermissionMode = "default"
			o.Environment["ALL_PROXY"] = "http://ignored-only.invalid:22"
			o.Environment["all_proxy"] = "http://ignored-lower.invalid:33"
			o.Native.Environment["ALL_PROXY"] = o.Environment["ALL_PROXY"]
			o.Native.Environment["all_proxy"] = o.Environment["all_proxy"]
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			path := filepath.Join(config, "settings.json")
			write := func(timeout int) {
				t.Helper()
				body := raw(map[string]any{"network": map[string]any{"proxy": map[string]any{"mode": "direct"}, "aiRequestTimeoutMs": timeout}, "future": map[string]any{"preserve": true}})
				if err := os.WriteFile(path, body, 0600); err != nil {
					t.Fatal(err)
				}
			}
			write(1_800_000)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()

			if desktop {
				live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(json.RawMessage) {})
				if err != nil {
					t.Fatal(err)
				}
				defer live.Close(context.Background())
				if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "first actual request"})); err != nil {
					t.Fatal(err)
				}
				write(45_000)
				if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "next request after network preference changed"})); err != nil {
					t.Fatal(err)
				}
			} else {
				s, err := NewSession(ctx, o)
				if err != nil {
					t.Fatal(err)
				}
				defer s.Close(context.Background())
				if err = s.Submit(ctx, tui.Input{Text: "ordinary actual request"}, nil); err != nil {
					t.Fatal(err)
				}
			}
			mu.Lock()
			got := append([]string(nil), timeout...)
			mu.Unlock()
			expected := []string{"600"}
			if desktop {
				expected = []string{"1800", "45"}
			}
			if !reflect.DeepEqual(got, expected) {
				t.Fatalf("actual timeout headers %v expected %v", got, expected)
			}

		})
	}
}

func TestActualDesktopNetworkMinimumTimeoutAndCancellationClosePendingHTTP(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		t.Run(map[bool]string{false: "explicit-stop", true: "configured-thirty-second-timeout"}[deadline], func(t *testing.T) {
			opened, closed := make(chan struct{}, 1), make(chan struct{}, 1)
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				if got := r.Header.Get("X-Stainless-Timeout"); got != "30" {
					t.Errorf("actual configured min timeout %s", got)
				}
				opened <- struct{}{}
				<-r.Context().Done()
				closed <- struct{}{}
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			if err := os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"network":{"aiRequestTimeoutMs":1,"proxy":{"mode":"direct"}}}`), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
			defer cancel()
			live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(json.RawMessage) {})
			if err != nil {
				t.Fatal(err)
			}
			done := make(chan error, 1)
			start := time.Now()
			go func() {
				done <- live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "pending actual HTTP"}))
			}()
			select {
			case <-opened:
			case <-ctx.Done():
				t.Fatal("actual request not opened")
			}
			if !deadline {
				if err = live.Handle(ctx, raw(map[string]any{"type": "stop_generation"})); err != nil {
					t.Fatal(err)
				}
			}
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("actual pending HTTP did not settle")
			}
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("real peer did not observe close")
			}
			if deadline && time.Since(start) < 29*time.Second {
				t.Fatal("configured timeout was bypassed", time.Since(start))
			}
			if err = live.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestActualDesktopFreshTeamPreferencesRenderOriginalTaskDescriptions(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "saved-opt-out", true: "saved-opt-in"}[enabled], func(t *testing.T) {
			flag := "0"
			if enabled {
				flag = "1"
			}
			source := actualDefaultTaskSource(t, []map[string]any{{"env": map[string]string{"USER_TYPE": "external", "CC_HAHA_AGENT_TEAMS_ENABLED": flag}, "cachedFeatures": map[string]any{"tengu_amber_flint": false}}})
			var request json.RawMessage
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				request, _ = io.ReadAll(r.Body)
				response(w, "team-prefs", "", nil, "actual team context")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Arguments.Tools = nil
			o.Arguments.Model = "deepseek-flash"
			config := o.Environment["CLAUDE_CONFIG_DIR"]
			_ = os.MkdirAll(config, 0700)
			if err := os.WriteFile(filepath.Join(config, "settings.json"), raw(map[string]any{"agentTeamsEnabled": enabled, "network": map[string]any{"proxy": map[string]any{"mode": "direct"}}}), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(json.RawMessage) {})
			if err != nil {
				t.Fatal(err)
			}
			defer live.Close(context.Background())
			if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "Actual preference request"})); err != nil {
				t.Fatal(err)
			}
			var body struct {
				Tools []struct{ Name, Description string }
			}
			if err = json.Unmarshal(request, &body); err != nil {
				t.Fatal(err)
			}
			found := map[string]string{}
			for _, tool := range body.Tools {
				found[tool.Name] = tool.Description
			}
			if found["TaskCreate"] != source.Rows[0].Create || found["TaskList"] != source.Rows[0].List {
				t.Fatal("actual Source task preference prompt mismatch")
			}
			if _, exists := found["TeamCreate"]; exists != enabled {
				t.Fatal("actual Team catalog gate diverged", found)
			}
		})
	}
}

func TestActualStandaloneAllProxyIsRetainedForBashWithoutRoutingTheModel(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if requests.Add(1) == 1 {
			response(w, "all-proxy-bash", "Bash", map[string]any{"command": "printf '%s\\n%s\\n' \"$ALL_PROXY\" \"$all_proxy\""}, "")
		} else {
			response(w, "all-proxy-final", "", nil, "actual shell completed")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Environment["ALL_PROXY"] = "http://must-not-route.invalid:22"
	o.Environment["all_proxy"] = "http://must-not-route-lower.invalid:33"
	o.Native.Environment["ALL_PROXY"], o.Native.Environment["all_proxy"] = o.Environment["ALL_PROXY"], o.Environment["all_proxy"]
	var output bytes.Buffer
	o.OnSDK = func(record json.RawMessage) error { output.Write(record); return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(ctx, tui.Input{Text: "Use actual Bash to read its two proxy variables."}, nil); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 2 {
		t.Fatal("ALL-only model failed real loopback route", requests.Load())
	}
	for _, value := range []string{o.Environment["ALL_PROXY"], o.Environment["all_proxy"]} {
		if !bytes.Contains(output.Bytes(), []byte(value)) {
			t.Fatal("real Bash lost original ALL proxy", value)
		}
	}
}

func TestActualDesktopNetworkUpdateDuringHTTPWaitKeepsOwnerAndSnapshot(t *testing.T) {
	firstOpened, releaseFirst := make(chan struct{}), make(chan struct{})
	var count atomic.Int32
	var mu sync.Mutex
	headers := []string{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		index := count.Add(1)
		mu.Lock()
		headers = append(headers, r.Header.Get("X-Stainless-Timeout"))
		mu.Unlock()
		if index == 1 {
			close(firstOpened)
			<-releaseFirst
		}
		response(w, "owner-network", "", nil, "real pending HTTP completed")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(config, 0700)
	settingsPath := filepath.Join(config, "settings.json")
	if err := os.WriteFile(settingsPath, []byte(`{"network":{"proxy":{"mode":"direct"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	live, err := DesktopFactory(o)(ctx, server.SessionOptions{ID: o.Arguments.SessionID, WorkDir: o.WorkDir}, func(json.RawMessage) {})
	if err != nil {
		t.Fatal(err)
	}
	d := live.(*DesktopSession)
	owner := d.engine.process.Owner
	controller := d.engine.process.Controller
	defer live.Close(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "first actual held request"}))
	}()
	select {
	case <-firstOpened:
	case <-ctx.Done():
		t.Fatal("actual HTTP did not open")
	}
	if err = d.engine.process.SetDesktopNetworkEnvironment(ctx, map[string]string{"API_TIMEOUT_MS": "45000", "CLAUDE_STREAM_FIRST_TOKEN_TIMEOUT_MS": "45000", "CLAUDE_STREAM_MAX_DURATION_MS": "600000"}); err != nil {
		close(releaseFirst)
		t.Fatal(err)
	}
	if err = d.engine.process.SetDesktopNetworkEnvironment(ctx, map[string]string{"ANTHROPIC_AUTH_TOKEN": "must-not-be-admitted"}); !errors.Is(err, cooperative.ErrRestricted) {
		close(releaseFirst)
		t.Fatal("network bridge admitted credential mutation", err)
	}
	close(releaseFirst)
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("original pending HTTP did not settle")
	}
	if err = os.WriteFile(settingsPath, []byte(`{"network":{"proxy":{"mode":"direct"},"aiRequestTimeoutMs":45000}}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err = live.Handle(ctx, raw(map[string]any{"type": "user_message", "content": "next actual request"})); err != nil {
		t.Fatal(err)
	}
	if d.engine.process.Owner != owner || d.engine.process.Controller != controller {
		t.Fatal("network update replaced original owner/controller")
	}
	mu.Lock()
	got := append([]string(nil), headers...)
	mu.Unlock()
	if !reflect.DeepEqual(got, []string{"1800", "45"}) {
		t.Fatal("in-flight snapshot or next request changed", got)
	}
	if err = live.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err = d.engine.process.SetDesktopNetworkEnvironment(ctx, map[string]string{"API_TIMEOUT_MS": "60000"}); !errors.Is(err, cooperative.ErrClosed) {
		t.Fatal("closed owner accepted network update", err)
	}
}

func TestActualOrdinaryCLIRejectsDesktopNetworkDomainWithoutChangingRequest(t *testing.T) {
	var timeout string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		timeout = r.Header.Get("X-Stainless-Timeout")
		response(w, "ordinary-guard", "", nil, "ordinary actual request")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.process.SetDesktopNetworkEnvironment(context.Background(), map[string]string{"API_TIMEOUT_MS": "45000"}); !errors.Is(err, cooperative.ErrRestricted) {
		t.Fatal("unselected optional network domain accepted update", err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "ordinary CLI request"}, nil); err != nil {
		t.Fatal(err)
	}
	if timeout != "600" {
		t.Fatal("ordinary CLI timeout was changed", timeout)
	}
}
