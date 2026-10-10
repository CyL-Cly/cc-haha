package migrations

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/settings"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type tracedFS struct {
	storage.FileSystem
	mu     sync.Mutex
	events []string
	fault  func(string, string) error
}

func (boundary *tracedFS) operation(operation, path string) error {
	boundary.mu.Lock()
	boundary.events = append(boundary.events, operation+":"+path)
	boundary.mu.Unlock()
	if boundary.fault != nil {
		return boundary.fault(operation, path)
	}
	return nil
}
func (boundary *tracedFS) ReadFile(path string) ([]byte, error) {
	if err := boundary.operation("read", path); err != nil {
		return nil, err
	}
	return boundary.FileSystem.ReadFile(path)
}
func (boundary *tracedFS) WriteFile(path string, data []byte, mode fs.FileMode) error {
	if err := boundary.operation("write", path); err != nil {
		return err
	}
	return boundary.FileSystem.WriteFile(path, data, mode)
}
func (boundary *tracedFS) MkdirAll(path string, mode fs.FileMode) error {
	if err := boundary.operation("mkdir", path); err != nil {
		return err
	}
	return boundary.FileSystem.MkdirAll(path, mode)
}
func (boundary *tracedFS) Rename(source, target string) error {
	if err := boundary.operation("rename", source+" -> "+target); err != nil {
		return err
	}
	return boundary.FileSystem.Rename(source, target)
}
func (boundary *tracedFS) CopyFile(source, target string) error {
	if err := boundary.operation("copy", source+" -> "+target); err != nil {
		return err
	}
	return boundary.FileSystem.CopyFile(source, target)
}
func (boundary *tracedFS) Remove(path string) error {
	if err := boundary.operation("unlink", path); err != nil {
		return err
	}
	return boundary.FileSystem.Remove(path)
}
func (boundary *tracedFS) access(path string) error {
	if err := boundary.operation("access", path); err != nil {
		return err
	}
	_, err := os.Stat(path)
	return err
}
func fixtureUpgrader(t *testing.T, boundary *tracedFS) *Upgrader {
	t.Helper()
	if boundary.FileSystem == nil {
		boundary.FileSystem = storage.New(storage.Options{}).FileSystem()
	}
	upgrader, err := New(Options{Store: storage.New(storage.Options{FS: boundary}), Access: boundary.access, Now: func() time.Time { return time.UnixMilli(123456) }, Random: bytes.NewReader(bytes.Repeat([]byte{1, 2, 3}, 100))})
	if err != nil {
		t.Fatal(err)
	}
	return upgrader
}
func put(t *testing.T, config, path, raw string) {
	t.Helper()
	absolute := filepath.Join(config, path)
	if err := os.MkdirAll(filepath.Dir(absolute), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(absolute, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func read(t *testing.T, config, path string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(config, path))
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}
func ensure(t *testing.T, upgrader *Upgrader, config string) Report {
	t.Helper()
	report, err := upgrader.Ensure(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	return report
}
func stable(t *testing.T, raw string) string {
	t.Helper()
	value, err := stableJSON(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	return string(value)
}

const oldProvider = `{"id":"custom","presetId":"custom","name":"Fixture","apiKey":"fake-key","baseUrl":"https://fixture.invalid","models":{"main":"main","haiku":"h","sonnet":"s","opus":"o"},"future":{"keep":true}}`

func TestOldIndexPreservesUnknownFieldsOrderAndBackupBytes(t *testing.T) {
	config := t.TempDir()
	old := `{"future":9007199254740993,"activeProviderId":"custom","providers":[` + oldProvider + `,{"invalid":true}],"schemaVersion":3,"providerOrder":["grok-official","custom","custom","unknown",4],"officialProviderModels":{"claude-official":{"main":"bad"},"future-slot":{"x":1}},"surrogate":"\ud800"}`
	put(t, config, "cc-haha/providers.json", old)
	put(t, config, "settings.json", `{"shared":"never migrate"}`)
	boundary := &tracedFS{}
	report := ensure(t, fixtureUpgrader(t, boundary), config)
	if !reflect.DeepEqual(report.MigratedEntries, []string{"cc-haha/providers.json"}) || len(report.Failures) != 0 {
		t.Fatal(report)
	}
	want := `{"future":9007199254740992,"providers":[{"id":"custom","presetId":"custom","name":"Fixture","apiKey":"fake-key","baseUrl":"https://fixture.invalid","models":{"main":"main","haiku":"h","sonnet":"s","opus":"o"},"future":{"keep":true},"toolSearchEnabled":false}],"schemaVersion":6,"surrogate":"\ud800","activeId":"custom","providerOrder":["grok-official","custom","claude-official","openai-official"],"officialProviderModels":{"future-slot":{"x":1}}}`
	if got := read(t, config, "cc-haha/providers.json"); got != stable(t, want) {
		t.Fatalf("migrated = %s", got)
	}
	if read(t, config, "cc-haha/providers.json.bak-before-migration-123456-010203") != old {
		t.Fatal("original backup bytes changed")
	}
	if read(t, config, "settings.json") != `{"shared":"never migrate"}` {
		t.Fatal("shared user settings changed")
	}
	operations := []string{}
	for _, event := range boundary.events {
		operations = append(operations, strings.SplitN(event, ":", 2)[0])
	}
	if !reflect.DeepEqual(operations, []string{"access", "read", "copy", "mkdir", "write", "rename", "read"}) {
		t.Fatal(operations)
	}
}

func TestEquivalentShapeWithDifferentKeyOrderStillRewritesLikeTS(t *testing.T) {
	config := t.TempDir()
	old := `{"providerOrder":["claude-official","openai-official","grok-official"],"schemaVersion":6,"activeId":null,"officialProviderModels":{},"providers":[],"future":"keep"}`
	put(t, config, "cc-haha/providers.json", old)
	report := ensure(t, fixtureUpgrader(t, &tracedFS{}), config)
	if len(report.MigratedEntries) != 1 {
		t.Fatal("semantic equality incorrectly suppressed source rewrite", report)
	}
	want := `{"schemaVersion":6,"activeId":null,"providers":[],"future":"keep","providerOrder":["claude-official","openai-official","grok-official"],"officialProviderModels":{}}`
	if read(t, config, "cc-haha/providers.json") != stable(t, want) {
		t.Fatal("rest field order changed")
	}
}

func TestManagedDeepSeekAndMalformedEnvPreserveFutureData(t *testing.T) {
	for _, test := range []struct {
		raw, want string
		changes   bool
	}{
		{`{"future":{"keep":true},"env":{"CC_HAHA_SEND_DISABLED_THINKING":"0","ANTHROPIC_BASE_URL":"https://api.deepseek.com/v1","KEEP":"yes"}}`, `{"future":{"keep":true},"env":{"ANTHROPIC_BASE_URL":"https://api.deepseek.com/v1","KEEP":"yes","ANTHROPIC_DEFAULT_FABLE_MODEL_SUPPORTED_CAPABILITIES":"thinking,effort,adaptive_thinking,xhigh_effort,max_effort","ANTHROPIC_DEFAULT_HAIKU_MODEL_SUPPORTED_CAPABILITIES":"thinking,effort,adaptive_thinking,xhigh_effort,max_effort","ANTHROPIC_DEFAULT_SONNET_MODEL_SUPPORTED_CAPABILITIES":"thinking,effort,adaptive_thinking,xhigh_effort,max_effort","ANTHROPIC_DEFAULT_OPUS_MODEL_SUPPORTED_CAPABILITIES":"thinking,effort,adaptive_thinking,xhigh_effort,max_effort"}}`, true},
		{`{"env":null,"future":true}`, `{"env":{},"future":true}`, true},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":false,"ANTHROPIC_BASE_URL":7},"future":true}`, `{"env":{"CC_HAHA_SEND_DISABLED_THINKING":false,"ANTHROPIC_BASE_URL":7},"future":true}`, false},
		{`{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":"deepseeK-x"}}`, `{"env":{"CC_HAHA_SEND_DISABLED_THINKING":"1","ANTHROPIC_MODEL":"deepseeK-x"}}`, false},
	} {
		config := t.TempDir()
		put(t, config, "cc-haha/settings.json", test.raw)
		report := ensure(t, fixtureUpgrader(t, &tracedFS{}), config)
		if len(report.Failures) != 0 || (len(report.MigratedEntries) > 0) != test.changes {
			t.Fatal(report)
		}
		want := test.raw
		if test.changes {
			want = stable(t, test.want)
		}
		if read(t, config, "cc-haha/settings.json") != want {
			t.Fatal("managed env drift")
		}
	}
}

func TestLegacyRootCreatesIndexAndManagedSettingsWithoutChangingLegacyOrShared(t *testing.T) {
	config := t.TempDir()
	legacy := `{"activeModel":"second","future":"original only","providers":[{"id":"legacy","name":"Fixture","apiKey":"fake","baseUrl":"https://fixture.invalid","models":[{"id":"first"},{"id":"second"}],"notes":{"keep":true},"future":"drop on mapped legacy"}]}`
	put(t, config, "providers.json", legacy)
	put(t, config, "settings.json", `{"futureUser":true}`)
	report := ensure(t, fixtureUpgrader(t, &tracedFS{}), config)
	if !reflect.DeepEqual(report.MigratedEntries, []string{"providers.json -> cc-haha/providers.json", "providers.json -> cc-haha/settings.json", "cc-haha/providers.json"}) || len(report.Failures) != 0 {
		t.Fatal(report)
	}
	if read(t, config, "providers.json") != legacy || read(t, config, "settings.json") != `{"futureUser":true}` {
		t.Fatal("original/shared state changed")
	}
	var index storage.Object
	_ = json.Unmarshal([]byte(read(t, config, "cc-haha/providers.json")), &index)
	if string(index["activeId"]) != `"legacy"` {
		t.Fatal(index)
	}
	if !strings.Contains(read(t, config, "cc-haha/settings.json"), `"ANTHROPIC_MODEL": "second"`) {
		t.Fatal("active model missing")
	}
}

func TestCorruptOwnedFilesStrictQuarantineAndSecondRunNormalization(t *testing.T) {
	config := t.TempDir()
	put(t, config, "cc-haha/providers.json", " \n")
	put(t, config, "cc-haha/settings.json", `{"env":`)
	upgrader := fixtureUpgrader(t, &tracedFS{})
	report := ensure(t, upgrader, config)
	if !reflect.DeepEqual(report.MigratedEntries, []string{"cc-haha/providers.json", "cc-haha/settings.json"}) || len(report.Failures) != 0 {
		t.Fatal(report)
	}
	if read(t, config, "cc-haha/providers.json") != "{}\n" || read(t, config, "cc-haha/providers.json.invalid-123456-010203") != " \n" {
		t.Fatal("quarantine bytes drift")
	}
	if ensure(t, upgrader, config).MigratedEntries[0] != "cc-haha/providers.json" {
		t.Fatal("cached report missing")
	}
	upgrader.ResetForTests()
	rerun := ensure(t, upgrader, config)
	if !reflect.DeepEqual(rerun.MigratedEntries, []string{"cc-haha/providers.json"}) {
		t.Fatal(rerun)
	}
}

func TestFailuresContinueWithoutExpandingRecoveryPolicy(t *testing.T) {
	for _, operation := range []string{"access", "read", "copy", "mkdir", "write", "rename", "unlink"} {
		t.Run(operation, func(t *testing.T) {
			config := t.TempDir()
			old := `{"providers":[]}`
			put(t, config, "cc-haha/providers.json", old)
			put(t, config, "cc-haha/settings.json", `{"env":null}`)
			mkdirCalls := 0
			boundary := &tracedFS{fault: func(op, path string) error {
				if operation == "mkdir" && op == "mkdir" {
					mkdirCalls++
					if mkdirCalls == 1 {
						return errors.New("fixture denied")
					}
				}
				if strings.Contains(path, "providers.json") && (op == operation || operation == "unlink" && op == "rename") {
					return errors.New("fixture denied")
				}
				return nil
			}}
			report := ensure(t, fixtureUpgrader(t, boundary), config)
			if len(report.Failures) == 0 || report.MigratedEntries[len(report.MigratedEntries)-1] != "cc-haha/settings.json" {
				t.Fatal(report)
			}
			if operation != "access" && read(t, config, "cc-haha/providers.json") != old {
				t.Fatal("failure changed original")
			}
			if operation == "mkdir" {
				for _, event := range boundary.events {
					if strings.HasPrefix(event, "unlink:") {
						t.Fatal("mkdir failure must not enter temp cleanup")
					}
				}
			}
		})
	}
	config := t.TempDir()
	put(t, config, "cc-haha/providers.json", `{`)
	put(t, config, "cc-haha/settings.json", `{"env":null}`)
	boundary := &tracedFS{fault: func(op, path string) error {
		if op == "rename" && strings.Contains(path, ".invalid-") {
			return fs.ErrPermission
		}
		return nil
	}}
	report := ensure(t, fixtureUpgrader(t, boundary), config)
	if len(report.Failures) != 1 || read(t, config, "cc-haha/providers.json") != `{` {
		t.Fatal(report)
	}
}

func TestMissingLegacyInvalidAndExistingManagedSettings(t *testing.T) {
	config := t.TempDir()
	boundary := &tracedFS{}
	report := ensure(t, fixtureUpgrader(t, boundary), config)
	if len(report.Failures) != 0 || len(report.MigratedEntries) != 0 {
		t.Fatal(report)
	}
	entries, _ := os.ReadDir(config)
	if len(entries) != 0 {
		t.Fatal("missing input created directories")
	}
	for _, raw := range []string{`{`, `null`, `{"providers":[]}`, `{"providers":[{"models":[]}]}`} {
		config := t.TempDir()
		put(t, config, "providers.json", raw)
		report := ensure(t, fixtureUpgrader(t, &tracedFS{}), config)
		if len(report.MigratedEntries) != 0 || read(t, config, "providers.json") != raw {
			t.Fatal(report)
		}
	}
	config = t.TempDir()
	put(t, config, "providers.json", `{"providers":[{"id":"p","name":"p","baseUrl":"b","apiKey":"k","models":[],"isActive":true}]}`)
	put(t, config, "cc-haha/settings.json", `{"future":"existing"}`)
	report = ensure(t, fixtureUpgrader(t, &tracedFS{}), config)
	if len(report.MigratedEntries) != 2 || read(t, config, "cc-haha/settings.json") != `{"future":"existing"}` {
		t.Fatal(report)
	}
}

func TestLastConfigCacheResetAndSettingsBootstrapUsesActualUpgrade(t *testing.T) {
	boundary := &tracedFS{}
	boundary.FileSystem = storage.New(storage.Options{}).FileSystem()
	sharedStore := storage.New(storage.Options{FS: boundary})
	upgrader, err := New(Options{Store: sharedStore, Access: boundary.access})
	if err != nil {
		t.Fatal(err)
	}
	first, second := t.TempDir(), t.TempDir()
	ensure(t, upgrader, first)
	count := len(boundary.events)
	ensure(t, upgrader, first)
	if len(boundary.events) != count {
		t.Fatal("same selected config reran")
	}
	ensure(t, upgrader, second)
	ensure(t, upgrader, first)
	if len(boundary.events) != count*3 {
		t.Fatal("A -> B -> A did not rerun A")
	}
	put(t, first, "cc-haha/settings.json", `{"env":null,"future":true}`)
	put(t, first, "settings.json", `{"futureUser":true}`)
	upgrader.ResetForTests()
	service, err := settings.NewManaged(settings.Options{Store: sharedStore, HomeDir: first, ConfigDir: first, Environment: func() map[string]string { return map[string]string{} }, EnsureMigrated: upgrader.SettingsCallback})
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.ReadSettings(context.Background())
	if err != nil || string(value["env"]) != "{}" {
		t.Fatalf("settings bootstrap = %v %v", value, err)
	}
	if !reflect.DeepEqual(ensure(t, upgrader, first).MigratedEntries, []string{"cc-haha/settings.json"}) {
		t.Fatal("service bypassed real migration")
	}
}

func TestConcurrentWaitersShareRunAndCancellationDoesNotAbortIt(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	boundary := &tracedFS{}
	upgrader := fixtureUpgrader(t, boundary)
	calls := 0
	upgrader.access = func(string) error { calls++; close(entered); <-release; return fs.ErrNotExist }
	config := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { _, err := upgrader.Ensure(ctx, config); result <- err }()
	<-entered
	cancel()
	if err := <-result; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	completed := make(chan Report, 1)
	go func() { report, _ := upgrader.Ensure(context.Background(), config); completed <- report }()
	close(release)
	report := <-completed
	if calls != 1 || len(report.Failures) != 0 {
		t.Fatal(calls, report)
	}
	if _, err := New(Options{}); err == nil {
		t.Fatal("nil shared store accepted")
	}
	if _, err := upgrader.Ensure(context.Background(), ""); err == nil {
		t.Fatal("ambient config fallback accepted")
	}
}

func TestActualUnreadableTemporaryFileIsReportedWithoutRepair(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX mode bits do not deny Windows file reads")
	}
	config := t.TempDir()
	put(t, config, "cc-haha/providers.json", `{"providers":[]}`)
	put(t, config, "cc-haha/settings.json", `{"env":null}`)
	path := filepath.Join(config, "cc-haha/providers.json")
	if err := os.Chmod(path, 0000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, 0600) })
	if _, err := os.ReadFile(path); !errors.Is(err, fs.ErrPermission) {
		t.Skip("test process can bypass file mode permission checks")
	}
	report := ensure(t, fixtureUpgrader(t, &tracedFS{}), config)
	if len(report.Failures) != 1 || !strings.HasPrefix(report.Failures[0], "cc-haha/providers.json: ") || !reflect.DeepEqual(report.MigratedEntries, []string{"cc-haha/settings.json"}) {
		t.Fatal(report)
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	if read(t, config, "cc-haha/providers.json") != `{"providers":[]}` {
		t.Fatal("unreadable protected source changed")
	}
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), "providers.json.") {
			t.Fatal("read permission failure created backup or quarantine", entry.Name())
		}
	}
}

func TestEntropyErrorsStayFailuresAndNeverOverwriteOriginals(t *testing.T) {
	for _, raw := range []string{`{"providers":[]}`, `{`} {
		config := t.TempDir()
		put(t, config, "cc-haha/providers.json", raw)
		upgrader, err := New(Options{Store: storage.New(storage.Options{}), Random: bytes.NewReader(nil)})
		if err != nil {
			t.Fatal(err)
		}
		report := ensure(t, upgrader, config)
		if !reflect.DeepEqual(report.Failures, []string{"cc-haha/providers.json: EOF"}) || read(t, config, "cc-haha/providers.json") != raw {
			t.Fatal(report)
		}
		if !reflect.DeepEqual(ensure(t, upgrader, config), report) {
			t.Fatal("failed result was silently retried")
		}
	}
	config := t.TempDir()
	put(t, config, "providers.json", `{"providers":[{"id":"p","name":"p","baseUrl":"b","apiKey":"k","models":[]}]}`)
	upgrader, err := New(Options{Store: storage.New(storage.Options{}), Random: bytes.NewReader(nil)})
	if err != nil {
		t.Fatal(err)
	}
	if report := ensure(t, upgrader, config); !reflect.DeepEqual(report.Failures, []string{"providers.json: EOF"}) || len(report.MigratedEntries) != 0 {
		t.Fatal(report)
	}
}

func TestResetDoesNotCancelAnAlreadyWaitingRun(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	boundary := &tracedFS{}
	upgrader := fixtureUpgrader(t, boundary)
	var accessMu sync.Mutex
	calls := 0
	upgrader.access = func(string) error {
		accessMu.Lock()
		calls++
		call := calls
		accessMu.Unlock()
		if call == 1 {
			close(entered)
			<-release
		}
		return fs.ErrNotExist
	}
	config := t.TempDir()
	first := make(chan Report, 1)
	go func() { report, _ := upgrader.Ensure(context.Background(), config); first <- report }()
	<-entered
	upgrader.ResetForTests()
	second := ensure(t, upgrader, config)
	close(release)
	if !reflect.DeepEqual(<-first, second) || calls != 2 {
		t.Fatal("reset cancelled old waiter or reused its pending run", calls)
	}
}
