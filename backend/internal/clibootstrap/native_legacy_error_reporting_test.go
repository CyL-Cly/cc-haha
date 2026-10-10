package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/errorlog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtintools"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
)

func TestLegacyKeychainErrorUsesPrivacyAwareReportingWithoutTerminalOutput(t *testing.T) {
	for _, env := range []map[string]string{{"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "0"}, {"CLAUDE_CODE_USE_BEDROCK": "true"}, {"DISABLE_ERROR_REPORTING": "0"}, {}} {
		for _, code := range []int{44, 73} {
			t.Run(fmt.Sprint(env, code), func(t *testing.T) {
				m := openAIAuthMainFixture(t)
				var terminal bytes.Buffer
				m.options.Error = &terminal
				calls := 0
				store, err := securestorage.New(securestorage.Options{Home: m.options.Native.Home, ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], ConfigDirExplicit: true, Platform: "darwin", Environment: m.options.Native.Environment, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
					calls++
					return nil, fmt.Errorf("private native Keychain failure: exit status %d", code)
				}})
				if err != nil {
					t.Fatal(err)
				}
				m.secure = store
				binding, err := m.NewNativeOpenAIAuth(nil)
				if err != nil {
					t.Fatal(err)
				}
				err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					for name, value := range env {
						if e := m.environment.SetProperty(quoteMain(name), quoteMain(value)); e != nil {
							return e
						}
					}
					owned, e := binding.owned(context.Background(), scope)
					if e != nil {
						return e
					}
					for range 2 {
						source, e := binding.apiKeySource(owned, env, false, false)
						if e != nil || source != "none" {
							return fmt.Errorf("legacy fallback source=%q: %v", source, e)
						}
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
				if calls != 1 {
					t.Fatalf("memoized native primitive calls=%d", calls)
				}
				if terminal.Len() != 0 {
					t.Fatalf("Source logError unexpectedly printed to terminal: %q", terminal.String())
				}
			})
		}
	}
}

func TestLegacyKeychainCancellationStillPropagatesWithoutReporting(t *testing.T) {
	m := openAIAuthMainFixture(t)
	store, err := securestorage.New(securestorage.Options{Home: m.options.Native.Home, ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], Platform: "darwin", Environment: m.options.Native.Environment, RunSecurity: func(ctx context.Context, _ securestorage.Command) ([]byte, error) { return nil, context.Canceled }})
	if err != nil {
		t.Fatal(err)
	}
	m.secure = store
	binding, err := m.NewNativeOpenAIAuth(nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, e := binding.owned(ctx, scope)
		if !errors.Is(e, context.Canceled) {
			t.Fatalf("cancellation erased: %v", e)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestLegacyKeychainFailureAndConfigFallbackAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	profiles := []map[string]any{}
	for _, env := range []map[string]string{{}, {"DISABLE_TELEMETRY": "1"}, {"DISABLE_ERROR_REPORTING": "0"}, {"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "false"}, {"CLAUDE_CODE_USE_VERTEX": " YES "}, {"DEBUG": "true"}} {
		for _, primary := range []string{"", "private-managed-config"} {
			profiles = append(profiles, map[string]any{"environment": env, "global": map[string]any{"primaryApiKey": primary}, "message": "private Keychain primitive failure: exit status 73", "stack": ""})
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-error-log.fixture.ts")
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_ERROR_LOG_FIXTURE=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(rawMain(map[string]any{"auth": profiles}))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatal(e, stderr.String())
	}
	var original struct {
		Rows []struct {
			First, Second *struct{ Key, Source string }
			Reads         int
			Memory        []errorlog.Row
			Sink          []errorlog.Error
		}
	}
	if e = json.Unmarshal(out, &original); e != nil || len(original.Rows) != len(profiles) {
		t.Fatal(e, string(out))
	}
	for i, p := range profiles {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			m := openAIAuthMainFixtureWithEnv(t, p["environment"].(map[string]string))
			m.options.Native.Now = func() time.Time { return time.UnixMilli(1700000000000) }
			var terminal bytes.Buffer
			m.options.Error = &terminal
			calls := 0
			store, e := securestorage.New(securestorage.Options{Home: m.options.Native.Home, ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], Platform: "darwin", Environment: m.options.Native.Environment, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
				calls++
				return nil, errors.New(p["message"].(string))
			}})
			if e != nil {
				t.Fatal(e)
			}
			m.secure = store
			binding, e := m.NewNativeOpenAIAuth(nil)
			if e != nil {
				t.Fatal(e)
			}
			var sources []string
			e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				for _, name := range []string{"DISABLE_ERROR_REPORTING", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI", "NODE_ENV", "USER_TYPE", "DEBUG", "DEBUG_SDK"} {
					if e := m.environment.SetProperty(quoteMain(name), nil); e != nil {
						return e
					}
				}
				for name, v := range p["environment"].(map[string]string) {
					if e := m.environment.SetProperty(quoteMain(name), quoteMain(v)); e != nil {
						return e
					}
				}
				if e := m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
					copy := current.Clone()
					copy.Set("primaryApiKey", rawMain(p["global"].(map[string]any)["primaryApiKey"]))
					return &copy, nil
				}); e != nil {
					return e
				}
				ctx, e := binding.owned(context.Background(), scope)
				if e != nil {
					return e
				}
				for range 2 {
					source, e := binding.apiKeySource(ctx, p["environment"].(map[string]string), false, false)
					if e != nil {
						return e
					}
					sources = append(sources, source)
				}
				return nil
			})
			if e != nil {
				t.Fatal(e)
			}
			want := original.Rows[i]
			expected := "none"
			key := ""
			if want.First != nil {
				expected = want.First.Source
				key = want.First.Key
			}
			second := "none"
			if want.Second != nil {
				second = want.Second.Source
			}
			if !reflect.DeepEqual(sources, []string{expected, second}) || binding.legacyAPIKey != key || calls != want.Reads || !reflect.DeepEqual(m.errorLog.Memory(), want.Memory) || terminal.Len() != 0 {
				t.Fatalf("Go sources%v key%q reads%d memory%v terminal%q Source%+v", sources, binding.legacyAPIKey, calls, m.errorLog.Memory(), terminal.String(), want)
			}
			m.closeNativeErrorLog()
			if p["environment"].(map[string]string)["DEBUG"] == "true" {
				path := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "debug", m.Controller.SessionID()+".txt")
				content, e := os.ReadFile(path)
				if e != nil || !strings.Contains(string(content), p["message"].(string)) {
					t.Fatalf("actual MainPrint debug sink path=%s: %v %q", path, e, content)
				}
				if info, e := os.Stat(path); e != nil || info.Mode().Perm() != 0600 {
					t.Fatalf("private debug mode: %v %v", info, e)
				}
			}
		})
	}
}

func TestLegacyKeychainPrimitiveCancellationPropagatesWithoutFallbackAndCanRetry(t *testing.T) {
	m := openAIAuthMainFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	store, e := securestorage.New(securestorage.Options{Home: m.options.Native.Home, ConfigDir: m.options.Native.Environment["CLAUDE_CONFIG_DIR"], Platform: "darwin", Environment: m.options.Native.Environment, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) {
		calls++
		if calls == 1 {
			cancel()
			return nil, context.Canceled
		}
		return []byte("private-managed-native"), nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	m.secure = store
	binding, e := m.NewNativeOpenAIAuth(nil)
	if e != nil {
		t.Fatal(e)
	}
	if e = m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		owned, e := binding.owned(ctx, scope)
		if e != nil {
			return e
		}
		_, e = binding.apiKeySource(owned, map[string]string{}, false, false)
		if !errors.Is(e, context.Canceled) || binding.legacyAPIKeyLoaded {
			t.Fatalf("cancel state e=%v loaded=%v", e, binding.legacyAPIKeyLoaded)
		}
		if m.errorLog != nil && len(m.errorLog.Memory()) != 0 {
			t.Fatal("canceled primitive was reported")
		}
		owned, e = binding.owned(context.Background(), scope)
		if e != nil {
			return e
		}
		source, e := binding.apiKeySource(owned, map[string]string{}, false, false)
		if e != nil || source != "/login managed key" || calls != 2 {
			t.Fatalf("actual retry e=%v source=%s calls%d", e, source, calls)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestNativeAuthErrorLogRejectsForeignOwnerAndShutdownFlushesActualJSONL(t *testing.T) {
	m := openAIAuthMainFixture(t)
	baseline := len(m.errorLog.Memory())
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error {
		ctx := context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope)
		if e := m.nativeLogError(ctx, errors.New("foreign must not write")); !errors.Is(e, cooperative.ErrRestricted) {
			t.Fatalf("foreign scope admitted: %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if len(m.errorLog.Memory()) != baseline {
		t.Fatal("foreign owner changed memory")
	}
	marker := "private same-owner diagnostic"
	if e := m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		for name, v := range map[string]string{"USER_TYPE": "ant", "NODE_ENV": "test", "DISABLE_ERROR_REPORTING": "", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": ""} {
			if e := m.environment.SetProperty(quoteMain(name), quoteMain(v)); e != nil {
				return e
			}
		}
		return m.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), errors.New(marker))
	}); e != nil {
		t.Fatal(e)
	}
	if e := m.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{}
	root := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "Cache")
	if e := filepath.WalkDir(root, func(path string, d os.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return nil
		}
		b, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			var row map[string]any
			if e := json.Unmarshal([]byte(line), &row); e != nil {
				return e
			}
			rows = append(rows, row)
		}
		info, e := os.Stat(path)
		if e != nil {
			return e
		}
		if info.Mode().Perm() != 0600 {
			t.Fatalf("private error JSONL perms %v", info.Mode().Perm())
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 1 || rows[0]["error"] != marker || rows[0]["cwd"] != m.options.Native.CWD || rows[0]["sessionId"] != m.Controller.SessionID() || rows[0]["userType"] != "ant" || rows[0]["version"] != m.options.Version {
		t.Fatalf("actual shutdown file rows=%v", rows)
	}
}

func TestIndependentAgentErrorLogUsesParentProducerAndChildCloseDoesNotDisposeIt(t *testing.T) {
	parent := openAIAuthMainFixture(t)
	var options MainPrintOptions
	if e := parent.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		options, e = parent.nativeIndependentAgentOptions(scope, builtintools.AgentRequest{AgentType: "general-purpose", Tools: []string{"Read"}, PermissionContext: protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoteMain("default")})}, "aprivateerrors", parent.options.Native.CWD, "fixture-model", parent.options.Native.Temp, nil, nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	child, e := NewMainPrint(options)
	if e != nil {
		t.Fatal(e)
	}
	defer child.ShutdownSession(context.Background())
	if e = child.StartSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if child.errorLog != nil || child.errorLogSink != nil {
		t.Fatal("in-process Agent created second logger")
	}
	baseline := len(parent.errorLog.Memory())
	if e = child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return child.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), errors.New("private child diagnostic"))
	}); e != nil {
		t.Fatal(e)
	}
	if len(parent.errorLog.Memory()) != baseline+1 || parent.errorLog.Memory()[baseline].Error != "private child diagnostic" {
		t.Fatal("parent process logger not consumed")
	}
	if e = child.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	if e = parent.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return parent.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), errors.New("parent after child close"))
	}); e != nil {
		t.Fatal(e)
	}
	if len(parent.errorLog.Memory()) != baseline+2 {
		t.Fatal("child disposed parent producer")
	}
}

func TestActualEarlyIndependentConstructorFailedLegacyLookupReportsOnParentAndKeepsConfigFallback(t *testing.T) {
	parent := openAIAuthMainFixture(t)
	baseline := len(parent.errorLog.Memory())
	var options MainPrintOptions
	if e := parent.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		if e := parent.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
			copy := current.Clone()
			copy.Set("primaryApiKey", quoteMain("private-old-managed-fallback"))
			copy.Set("futureLoggerFixture", rawMain(map[string]any{"keep": true}))
			return &copy, nil
		}); e != nil {
			return e
		}
		var e error
		options, e = parent.nativeIndependentAgentOptions(scope, builtintools.AgentRequest{AgentType: "general-purpose", Tools: []string{"Read"}, PermissionContext: protocol.NewJSONObject(protocol.JSONField{Name: "mode", Value: quoteMain("default")})}, "aprivateearlyerror", parent.options.Native.CWD, "fixture-model", parent.options.Native.Temp, nil, nil)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	options.Interactive = true
	options.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("private failed early-constructor lookup")
	}
	child, e := NewMainPrint(options)
	if e != nil {
		t.Fatal("actual early child constructor", e)
	}
	defer child.ShutdownSession(context.Background())
	if !child.openAIAuth.legacyAPIKeyLoaded || child.openAIAuth.legacyAPIKey != "private-old-managed-fallback" {
		t.Fatalf("original config fallback not selected: loaded%v key%q", child.openAIAuth.legacyAPIKeyLoaded, child.openAIAuth.legacyAPIKey)
	}
	memory := parent.errorLog.Memory()
	if len(memory) != baseline+1 || memory[baseline].Error != "private failed early-constructor lookup" {
		t.Fatalf("actual early error delivery %v", memory)
	}
	if child.errorLog != nil || child.errorLogSink != nil {
		t.Fatal("early child created second logger")
	}
	if e = child.StartSession(context.Background()); e != nil {
		t.Fatal("actual child starts after recoverable lookup", e)
	}
	if e = child.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
}

func TestEarlyErrorTransferChildOwnerCloseReleasesFutureWhileParentDeliveryIsHeld(t *testing.T) {
	parent := openAIAuthMainFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	held, e := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { close(entered); <-release; return nil })
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	child := &MainPrint{Owner: cooperative.New(), options: MainPrintOptions{DetachedAgent: &NativeDetachedAgentOptions{authParent: parent}}}
	defer child.Owner.Close()
	waiting := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			if _, e := scope.ArmTimer(time.Millisecond, func() { close(waiting) }); e != nil {
				return e
			}
			return child.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), errors.New("private close-held transfer"))
		})
	}()
	select {
	case <-waiting:
	case e := <-done:
		close(release)
		t.Fatalf("transfer returned before owner Future wait: %v", e)
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("owner Future did not admit real timer while awaiting transfer")
	}
	child.Owner.Close()
	select {
	case e := <-done:
		if !errors.Is(e, cooperative.ErrClosed) {
			close(release)
			t.Fatalf("child lifetime lost: %v", e)
		}
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("closing child did not release its actual Future")
	}
	close(release)
	if e := held.Wait(); e != nil {
		t.Fatal(e)
	}
	// This actual Read receipt is ordered after the original error delivery and
	// observes its completion. No lock-external parent metadata read is used.
	r, e := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		memory := parent.errorLog.Memory()
		if len(memory) == 0 || memory[len(memory)-1].Error != "private close-held transfer" {
			return errors.New("original parent delivery not completed after child close")
		}
		return nil
	})
	if e == nil {
		e = r.Wait()
	}
	if e != nil {
		t.Fatal(e)
	}
}

func TestFailedParentErrorBackendDoesNotBreakSourceRecoverableFallback(t *testing.T) {
	parent := openAIAuthMainFixture(t)
	if e := parent.ShutdownSession(context.Background()); e != nil {
		t.Fatal(e)
	}
	child := &MainPrint{Owner: cooperative.New(), options: MainPrintOptions{DetachedAgent: &NativeDetachedAgentOptions{authParent: parent}}}
	defer child.Owner.Close()
	if e := child.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		return child.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), errors.New("closed diagnostic backend"))
	}); e != nil {
		t.Fatal("Source catches reporting backend failure", e)
	}
}

func TestEarlyErrorTransferCallerScopeCancelKeepsParentDeliveryOnItsOwnOwner(t *testing.T) {
	parent := openAIAuthMainFixture(t)
	entered, release := make(chan struct{}), make(chan struct{})
	held, e := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { close(entered); <-release; return nil })
	if e != nil {
		t.Fatal(e)
	}
	<-entered
	child := &MainPrint{Owner: cooperative.New(), options: MainPrintOptions{DetachedAgent: &NativeDetachedAgentOptions{authParent: parent}}}
	defer child.Owner.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- child.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			if _, e := scope.ArmTimer(time.Millisecond, cancel); e != nil {
				return e
			}
			return child.nativeLogError(context.WithValue(context.Background(), nativeOpenAIAuthScopeKey{}, scope), errors.New("private canceled reporting caller"))
		})
	}()
	select {
	case e := <-done:
		if !errors.Is(e, context.Canceled) {
			close(release)
			t.Fatalf("caller cancellation changed: %v", e)
		}
	case <-time.After(10 * time.Second):
		close(release)
		t.Fatal("caller cancellation did not release Future await")
	}
	close(release)
	if e := held.Wait(); e != nil {
		t.Fatal(e)
	}
	r, e := parent.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error {
		memory := parent.errorLog.Memory()
		if len(memory) == 0 || memory[len(memory)-1].Error != "private canceled reporting caller" {
			return errors.New("parent metadata was not delivered on real Read")
		}
		return nil
	})
	if e == nil {
		e = r.Wait()
	}
	if e != nil {
		t.Fatal(e)
	}
}
