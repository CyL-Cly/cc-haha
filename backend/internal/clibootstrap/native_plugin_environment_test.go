package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativePluginPolicyReadsLiveCanonicalProcessEnvironment(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source Bun is required")
	}
	var requests atomic.Int32
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		t.Error("synchronous policy mutation consumer attempted HTTP", r.Method, r.URL)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer proxy.Close()
	fallback := map[string]any{"env": map[string]any{"FIXTURE_POLICY": "local"}, "future": map[string]any{"keep": []any{true, "managed"}}}
	cached := map[string]any{"env": map[string]any{"FIXTURE_POLICY": "remote"}, "future": map[string]any{"keep": []any{true, "remote"}}}
	bases := []string{"", "http://127.0.0.1:1", "https://api.anthropic.com"}
	// Independent disposable Source and Go states start with the same actual
	// custom-base initial eligibility. Neither side resets any cache between
	// the three assignments; the Source policy consumer is the authority.
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err = os.Chmod(home, 0700); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(home, "tmp")
	if err = os.Mkdir(tmp, 0700); err != nil {
		t.Fatal(err)
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-remote-managed-policy.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + tmp, "PATH=/usr/bin:/bin", "NODE_ENV=production", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_REMOTE_MANAGED_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=0", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "ANTHROPIC_API_KEY=private-ultracode-fake-key", "HTTPS_PROXY=" + proxy.URL}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"action": "policy-mutations", "bases": bases, "fallback": fallback, "cached": cached, "user": map[string]any{"enabledPlugins": map[string]any{"sample@mkt": false}}}))
	var diagnostics bytes.Buffer
	command.Stderr = &diagnostics
	out, err := command.Output()
	if err != nil {
		t.Fatalf("actual original policy mutation consumer: %v\n%s\n%s", err, out, diagnostics.String())
	}
	var source map[string]any
	if err = json.Unmarshal(out, &source); err != nil {
		t.Fatal(err, string(out))
	}
	caps, ok := source["caps"].(map[string]any)
	if !ok || len(caps) != 3 {
		t.Fatal("actual Source capabilities shape", string(out))
	}
	for _, name := range []string{"blocked", "secureReads", "absentReads"} {
		values, ok := caps[name].([]any)
		if !ok || len(values) != 0 {
			t.Fatal("actual synchronous Source policy escaped private/no-network scope", name, string(out))
		}
	}
	f := newNativePluginFixture(t)
	f.n.Environment["ANTHROPIC_API_KEY"] = "private-ultracode-fake-key"
	f.n.Environment["HTTPS_PROXY"] = proxy.URL
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false}}`)
	f.write(t, filepath.Join(f.managed, "managed-settings.json"), string(rawMain(fallback)))
	f.write(t, filepath.Join(f.config, "remote-settings.json"), string(rawMain(cached)))
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Sources: DefaultState(), SystemPrompt: "private policy memo consumer", Tools: []string{""}, MaxTurns: 1, PermissionMode: "default"}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	read := func(base string) (map[string]any, error) {
		var result map[string]any
		err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			if err := m.environment.SetProperty(quoteMain("ANTHROPIC_BASE_URL"), quoteMain(base)); err != nil {
				return err
			}
			policy, err := m.options.Native.ResolvePolicy()
			if err != nil {
				return err
			}
			s := m.remoteManaged
			s.mu.Lock()
			if s.eligible == nil {
				s.mu.Unlock()
				return fmt.Errorf("actual native startup did not prepare policy eligibility")
			}
			eligible := *s.eligible
			s.mu.Unlock()
			result = map[string]any{"base": base, "eligible": eligible, "leaf": s.snapshot(), "policy": policy}
			return nil
		})
		return result, err
	}
	initial, err := read("http://127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	rows := []any{}
	for _, base := range bases {
		row, err := read(base)
		if err != nil {
			t.Fatal(err)
		}
		rows = append(rows, row)
	}
	actual := map[string]any{"initial": initial, "rows": rows}
	expected := map[string]any{}
	for key, value := range source {
		if key != "caps" {
			expected[key] = value
		}
	}
	var canonical any
	if err := json.Unmarshal(rawMain(actual), &canonical); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(canonical, expected) {
		t.Fatalf("actual Source policy eligibility/cache consumer mismatch\nGo %s\nSource %s", rawMain(actual), out)
	}
	if requests.Load() != 0 {
		t.Fatal("synchronous policy mutation requested HTTP", requests.Load())
	}
	t.Logf("PLUGIN_POLICY_SOURCE_MEMO %s", out)
}

func TestNativePluginEnvironmentIndependentReadersAndOwnerAssignments(t *testing.T) {
	m := ultracodeMain(t, "http://127.0.0.1:1", "", true, nil)
	ctx := context.Background()
	start := make(chan struct{})
	failures := make(chan error, 4)
	var readers sync.WaitGroup
	for range 4 {
		readers.Add(1)
		go func() {
			defer readers.Done()
			<-start
			for range 200 {
				if _, err := m.options.Native.ResolvePolicy(); err != nil {
					failures <- err
					return
				}
				if value, ok := m.options.Native.nativeEnvironmentValue("FIXTURE_LIVE"); ok && value != "" && value != "a" && value != "b" {
					failures <- fmt.Errorf("torn native environment value %q", value)
					return
				}
				m.options.Native.nativeEnvironmentSnapshot()
			}
		}()
	}
	close(start)
	if err := m.Owner.Run(ctx, func(_ *cooperative.Scope) error {
		for range 200 {
			for _, value := range []string{"a", "b"} {
				if err := m.environment.Assign(protocol.NewJSONObject(fieldMain("FIXTURE_LIVE", value))); err != nil {
					return err
				}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	readers.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
}
