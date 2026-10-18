//go:build darwin

package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/fsnotify/fsnotify"
)

// A real FIFO remains connected and empty until the actual init consumer has
// reached its Owner FIFO observer. The old Host emits init from stale registry
// strings; the Source QueryEngine waits for the real command-loader Promise.
func TestActualQueryEngineInitJoinsHeldSkillProducerAfterCacheClear(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`)
	skill := filepath.Join(f.cwd, ".claude", "skills", "held", "SKILL.md")
	payload := "---\ndescription: Actual held init skill\n---\nActual native catalog body."
	f.write(t, skill, payload)
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Error("unowned model request", r.Method, r.URL.String())
			http.Error(w, "unowned", 403)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Contains(body, []byte("ACTUAL_INIT_CATALOG_USER")) {
			t.Errorf("actual model body: %v %s", err, body)
		}
		index := posts.Add(1)
		emitPlanFixture(w, int(index), "", "ACTUAL_INIT_CATALOG_DONE")
	}))
	defer peer.Close()
	fifo := filepath.Join(f.home, "source-skill.fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	probe := filepath.Join(f.home, "source-held-probe.json")
	armed := filepath.Join(f.home, "source-fifo-armed")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err = watcher.Add(f.home); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-query-init-catalog.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_QUERY_INIT_CATALOG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-init-catalog", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=" + filepath.Join(f.home, "bin")}
	input, _ := json.Marshal(map[string]string{"home": f.home, "peer": peer.URL, "skill": skill, "fifo": fifo, "probe": probe, "armed": armed})
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	sourceWait := &queryCatalogSourceWait{command: command, done: make(chan struct{}), stdout: &stdout, stderr: &stderr}
	go func() { sourceWait.failure = command.Wait(); close(sourceWait.done) }()
	defer func() {
		if !sourceWait.stopAndJoin() {
			t.Error("Source held cleanup: private child Wait did not join within 3s")
		}
	}()
	queryCatalogWaitSourceFile(t, ctx, watcher, armed, sourceWait)
	info, err := os.Lstat(skill)
	if err != nil || info.Mode()&os.ModeNamedPipe == 0 {
		t.Fatalf("Source did not arm actual skill FIFO: %v %v %s", info, err, sourceWait.raw())
	}
	writer := queryCatalogHeldSourceWriter(t, ctx, f.home, skill, sourceWait)
	defer writer.Close()
	// The Source queueMicrotask is independent of the held real read. Observe
	// its physical file through actual directory events, never a scheduling wait.
	rawProbe := queryCatalogWaitSourceFile(t, ctx, watcher, probe, sourceWait)
	if string(rawProbe) != `{"init":false}` {
		_ = writer.Close()
		t.Fatalf("Source init crossed held loader: probe=%s %s", rawProbe, sourceWait.raw())
	}
	// As in the cold fixture, keep the original read descriptor held while
	// subsequent original opens see identical real bytes at a regular path.
	if err := queryCatalogRestorePrivateFile(skill, []byte(payload)); err != nil {
		t.Fatalf("Source held path restore: %v probe=%s %s", err, rawProbe, sourceWait.raw())
	}
	exitedBeforeWrite := false
	select {
	case <-sourceWait.done:
		exitedBeforeWrite = true
	default:
	}
	_, writeErr := io.WriteString(writer, payload)
	closeErr := writer.Close()
	if writeErr != nil || closeErr != nil {
		exitedAfterWrite := false
		select {
		case <-sourceWait.done:
			exitedAfterWrite = true
		default:
		}
		t.Fatalf("Source held FIFO release: write=%v close=%v exitedBeforeWrite=%v exitedAfterWrite=%v probe=%s %s", writeErr, closeErr, exitedBeforeWrite, exitedAfterWrite, rawProbe, sourceWait.raw())
	}
	select {
	case <-sourceWait.done:
		if sourceWait.failure != nil {
			t.Fatalf("Source init after FIFO release: probe=%s %s", rawProbe, sourceWait.raw())
		}
	case <-ctx.Done():
		t.Fatalf("Source init after FIFO release: %v probe=%s %s", ctx.Err(), rawProbe, sourceWait.raw())
	}
	var source struct {
		Init                                               protocol.JSONObject
		WarmSkillsSame, ClearedSkillsDifferent, PluginSame bool
		SameRecordPromise                                  []bool
		Blocked                                            []string
	}
	if err := json.Unmarshal(stdout.Bytes(), &source); err != nil || len(source.Blocked) > 0 || !source.WarmSkillsSame || !source.ClearedSkillsDifferent || !source.PluginSame {
		t.Fatalf("Source actual init/caches: %v %s %s", err, stdout.String(), stderr.String())
	}
	for _, same := range source.SameRecordPromise {
		if !same {
			t.Fatal("Source observer replaced actual Record Promise")
		}
	}
	if !bytes.Contains(source.Init.Get("skills"), []byte(`"held"`)) {
		t.Fatal("Source init did not consume real held skill", source.Init)
	}
	// Restore a real regular file for the constructor's actual startup load.
	if err := os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	f.write(t, skill, payload)
	f.n.Environment["ANTHROPIC_BASE_URL"], f.n.Environment["ANTHROPIC_API_KEY"] = peer.URL, "offline-init-catalog"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	var init atomic.Bool
	observed := make(chan bool, 1)
	var once sync.Once
	var m *MainPrint
	disabled := false
	options := MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard,
		Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual init catalog", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"},
		OnOutput: func(raw []byte) error {
			var value protocol.JSONObject
			if value.UnmarshalJSON(raw) == nil && value.String("type") == "system" && value.String("subtype") == "init" {
				init.Store(true)
			}
			return nil
		},
		OnSDKPhase: func(phase string) {
			if phase == "Record:fulfilled" {
				once.Do(func() {
					_, err := m.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { observed <- init.Load(); return nil })
					if err != nil {
						observed <- true
					}
				})
			}
		},
	}
	m, err = NewMainPrint(options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = m.ShutdownSession(closeCtx)
	})
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	if err = os.Remove(skill); err != nil {
		t.Fatal(err)
	}
	if err = syscall.Mkfifo(skill, 0600); err != nil {
		t.Fatal(err)
	}
	m.skillCommands.Clear()
	m.invalidateNativeCommandCatalog()
	loading := make(chan error, 1)
	go func() { _, _, err := m.nativeCommandDefinitions(ctx); loading <- err }()
	nativeWriter := queryCatalogFIFOConnectedWriter(t, ctx, skill)
	defer nativeWriter.Close()
	turn := make(chan error, 1)
	go func() {
		turn <- m.RunTurn(ctx, objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "ACTUAL_INIT_CATALOG_USER"}})))
	}()
	var crossed bool
	select {
	case crossed = <-observed:
	case err := <-turn:
		t.Fatalf("native turn returned before init observation: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	_, writeErr = io.WriteString(nativeWriter, payload)
	closeErr = nativeWriter.Close()
	if writeErr != nil || closeErr != nil {
		t.Fatal(writeErr, closeErr)
	}
	select {
	case err = <-loading:
		if err != nil {
			t.Fatal("actual catalog producer", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	select {
	case err = <-turn:
		if err != nil {
			t.Fatal("actual native turn", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if crossed {
		t.Fatal("native init used registry snapshot before actual held skill producer completed")
	}
	if !init.Load() || posts.Load() != 2 {
		t.Fatal("actual init/one POST per Source and Native", init.Load(), posts.Load())
	}
}

// This one held Source arm must observe early child exits while its actual
// O_WRONLY opener waits. Cleanup rescues only this exact private FIFO and joins
// the opener after killing/joining the original Source process.
func queryCatalogHeldSourceWriter(t *testing.T, ctx context.Context, privateHome, path string, source *queryCatalogSourceWait) *os.File {
	t.Helper()
	relative, err := filepath.Rel(privateHome, path)
	info, statError := os.Lstat(path)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || statError != nil || info.Mode()&os.ModeNamedPipe == 0 || info.Mode()&os.ModeSymlink != 0 {
		t.Fatalf("Source held private FIFO rejected %s: %v %v %s", path, err, statError, source.raw())
	}
	type opened struct {
		file *os.File
		err  error
	}
	ready := make(chan opened, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		file, err := os.OpenFile(path, os.O_WRONLY, 0600)
		ready <- opened{file, err}
	}()
	received := false
	defer func() {
		if received {
			return
		}
		if !source.stopAndJoin() {
			t.Error("Source held opener cleanup: child Wait did not join within 3s")
		}
		rescue, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
		if err != nil {
			t.Errorf("Source held private FIFO rescue: %v", err)
		} else {
			defer rescue.Close()
		}
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		select {
		case value := <-ready:
			if value.file != nil {
				_ = value.file.Close()
			}
		case <-deadline.C:
			t.Error("Source held writer opener did not return within 3s cleanup")
			return
		}
		select {
		case <-joined:
		case <-deadline.C:
			t.Error("Source held writer opener did not join within 3s cleanup")
		}
	}()
	select {
	case value := <-ready:
		received = true
		<-joined
		if value.err != nil {
			t.Fatalf("Source held writer open: %v %s", value.err, source.raw())
		}
		return value.file
	case <-source.done:
		t.Fatalf("Source setup/guard exited before held writer rendezvous: %s", source.raw())
	case <-ctx.Done():
		t.Fatalf("Source held writer rendezvous: %v %s", ctx.Err(), source.raw())
	}
	return nil
}

func queryCatalogFIFOConnectedWriter(t *testing.T, ctx context.Context, path string) *os.File {
	t.Helper()
	type opened struct {
		file *os.File
		err  error
	}
	ready := make(chan opened, 1)
	go func() { file, err := os.OpenFile(path, os.O_WRONLY, 0600); ready <- opened{file, err} }()
	select {
	case value := <-ready:
		if value.err != nil {
			t.Fatal(value.err)
		}
		return value.file
	case <-ctx.Done():
		// Cleanup only: unblock the private writer opener if Source setup failed
		// before owning the actual read descriptor. It supplies no business bytes.
		if reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0); err == nil {
			defer reader.Close()
			value := <-ready
			if value.file != nil {
				_ = value.file.Close()
			}
		}
		t.Fatal(ctx.Err())
		return nil
	}
}

func queryCatalogWaitFile(t *testing.T, ctx context.Context, watcher *fsnotify.Watcher, path string, child <-chan error, stdout, stderr *bytes.Buffer) []byte {
	t.Helper()
	for {
		if raw, err := os.ReadFile(path); err == nil {
			return raw
		}
		select {
		case _, open := <-watcher.Events:
			if !open {
				t.Fatal("actual catalog filesystem watcher closed")
			}
		case err := <-watcher.Errors:
			t.Fatal("actual catalog filesystem watcher", err)
		case err := <-child:
			t.Fatalf("actual Source exited before physical %s: %v %s %s", path, err, stdout.String(), stderr.String())
		case <-ctx.Done():
			t.Fatal("actual catalog filesystem event", ctx.Err())
			return nil
		}
	}
}

// This distinct cold regression is expected to remain RED until the real local,
// plugin, and workflow producers are started independently. The warm init join
// test above does not establish this source-internal fan-out boundary.
func TestActualColdCommandCatalogStartsLocalReadWhilePluginReadIsHeld(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	f.n.Environment["ANTHROPIC_API_KEY"] = "offline-cold-catalog"
	// Preserve the actual marketplace reads without contributing an active
	// plugin settings base to the finite usercontext absent profile.
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"future":{"keep":true}}`)
	skill := filepath.Join(f.cwd, ".claude", "skills", "held", "SKILL.md")
	payload := "---\ndescription: Actual independent local producer\n---\nPhysical local skill."
	f.write(t, skill, payload)
	pluginPath := filepath.Join(f.config, "plugins", "known_marketplaces.json")
	known, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(f.home, "source-local.fifo"), filepath.Join(f.home, "source-plugin.fifo")} {
		if err = syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
	}
	armed := filepath.Join(f.home, "source-dual-armed")
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		t.Fatal(err)
	}
	defer watcher.Close()
	if err = watcher.Add(f.home); err != nil {
		t.Fatal(err)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-query-init-catalog.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_QUERY_INIT_CATALOG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-cold-catalog", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=" + filepath.Join(f.home, "bin")}
	input, _ := json.Marshal(map[string]any{"home": f.home, "peer": "http://127.0.0.1:1", "dualFIFO": true, "skill": skill, "fifo": filepath.Join(f.home, "source-local.fifo"), "pluginPath": pluginPath, "pluginFifo": filepath.Join(f.home, "source-plugin.fifo"), "armed": armed})
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	sourceWait := &queryCatalogSourceWait{command: command, done: make(chan struct{}), stdout: &stdout, stderr: &stderr}
	go func() { sourceWait.failure = command.Wait(); close(sourceWait.done) }()
	defer func() {
		if !sourceWait.stopAndJoin() {
			t.Error("Source cleanup: private child Wait did not join within 3s")
		}
	}()
	queryCatalogWaitSourceFile(t, ctx, watcher, armed, sourceWait)
	sourceParallel := queryCatalogReleaseDualFIFO(t, ctx, f.home, pluginPath, skill, known, []byte(payload), sourceWait)
	select {
	case <-sourceWait.done:
		if sourceWait.failure != nil {
			t.Fatalf("Source setup/guard failure after real FIFO release: %s", sourceWait.raw())
		}
	case <-ctx.Done():
		t.Fatalf("Source setup/guard completion timeout: %v %s", ctx.Err(), sourceWait.raw())
	}
	var source struct{ Names, Blocked []string }
	if err = json.Unmarshal(stdout.Bytes(), &source); err != nil || len(source.Blocked) > 0 || !sourceParallel || !strings.Contains(string(rawMain(source.Names)), `"held"`) {
		t.Fatalf("actual Source dual FIFO %v parallel=%v stdout=%s stderr=%s", err, sourceParallel, stdout.String(), stderr.String())
	}
	for path, body := range map[string][]byte{skill: []byte(payload), pluginPath: known} {
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err = os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private cold fan-out", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = m.ShutdownSession(closeCtx)
	})
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	// The startup installer also reads known_marketplaces. Join that real
	// producer before arming catalog-only rendezvous at the same private path.
	queryCatalogJoinStartupInstallation(t, ctx, m)
	for _, path := range []string{skill, pluginPath} {
		if err = os.Remove(path); err != nil {
			t.Fatal(err)
		}
		if err = syscall.Mkfifo(path, 0600); err != nil {
			t.Fatal(err)
		}
	}
	m.skillCommands.Clear()
	m.invalidateNativeCommandCatalog()
	m.nativeQueryCatalog.loader.Clear()
	loaded := make(chan error, 1)
	nativeDone := make(chan struct{})
	defer func() {
		cancel()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		select {
		case <-nativeDone:
		case <-deadline.C:
			t.Error("native cold producer cleanup did not join within 3s")
		}
	}()
	go func() {
		defer close(nativeDone)
		definitions, _, err := m.nativeCommandDefinitions(ctx)
		if err == nil && queryCatalogDescription(definitions, "held") == "" {
			err = io.ErrUnexpectedEOF
		}
		loaded <- err
	}()
	nativeParallel := queryCatalogReleaseDualFIFO(t, ctx, f.home, pluginPath, skill, known, []byte(payload), nil)
	select {
	case err = <-loaded:
		if err != nil {
			t.Fatal("native actual cold producer", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if !nativeParallel {
		t.Fatal("native local skill read did not enter before the independent real plugin read was released; Source entered both")
	}
}

// sourceWait owns the one original Cmd.Wait. Early failure kills and joins it
// before reading buffers, so setup diagnostics neither race pipe copying nor
// leave a private child behind. This timeout is cleanup-only, not a business wait.
type queryCatalogSourceWait struct {
	command        *exec.Cmd
	done           chan struct{}
	failure        error
	stdout, stderr *bytes.Buffer
}

func (source *queryCatalogSourceWait) stopAndJoin() bool {
	select {
	case <-source.done:
		return true
	default:
		_ = source.command.Process.Kill()
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	select {
	case <-source.done:
		return true
	case <-deadline.C:
		return false
	}
}

func (source *queryCatalogSourceWait) raw() string {
	if !source.stopAndJoin() {
		return "Source Wait not joined; live output buffers were not read"
	}
	return "wait=" + errorQueryCatalogString(source.failure) + " stdout=" + source.stdout.String() + " stderr=" + source.stderr.String()
}

func errorQueryCatalogString(err error) string {
	if err == nil {
		return "nil"
	}
	return err.Error()
}

func queryCatalogWaitSourceFile(t *testing.T, ctx context.Context, watcher *fsnotify.Watcher, path string, source *queryCatalogSourceWait) []byte {
	t.Helper()
	for {
		if raw, err := os.ReadFile(path); err == nil {
			return raw
		}
		select {
		case _, open := <-watcher.Events:
			if !open {
				t.Fatalf("Source setup watcher closed: %s", source.raw())
			}
		case err := <-watcher.Errors:
			t.Fatalf("Source setup watcher error: %v %s", err, source.raw())
		case <-source.done:
			t.Fatalf("Source setup/guard exited before %s: %s", path, source.raw())
		case <-ctx.Done():
			t.Fatalf("Source setup/guard timed out before %s: %v %s", path, ctx.Err(), source.raw())
		}
	}
}

func queryCatalogReleaseDualFIFO(t *testing.T, ctx context.Context, privateHome, pluginPath, skillPath string, known, payload []byte, source *queryCatalogSourceWait) bool {
	t.Helper()
	type opened struct {
		file  *os.File
		err   error
		index int
	}
	paths := []string{pluginPath, skillPath}
	for _, path := range paths {
		relative, err := filepath.Rel(privateHome, path)
		info, statError := os.Lstat(path)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(os.PathSeparator)) || statError != nil || info.Mode()&os.ModeNamedPipe == 0 || info.Mode()&os.ModeSymlink != 0 {
			t.Fatalf("private FIFO setup rejected %s: %v %v", path, err, statError)
		}
	}
	ready := make(chan opened, 2)
	var files [2]*os.File
	var received [2]bool
	var joined [2]chan struct{}
	for index, path := range paths {
		index, path := index, path
		joined[index] = make(chan struct{})
		go func() {
			defer close(joined[index])
			file, err := os.OpenFile(path, os.O_WRONLY, 0600)
			ready <- opened{file, err, index}
		}()
	}
	defer func() {
		// Only an already-failed setup/operation reaches here with outstanding
		// openers. Rescue exact owned FIFO readers, then restore real regular
		// paths BEFORE closing writers, so a late original reader cannot enter
		// an abandoned FIFO inode. No cleanup bytes enter a held business read.
		var rescue [2]*os.File
		for index, path := range paths {
			if !received[index] {
				reader, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
				if err != nil {
					t.Errorf("private FIFO cleanup rescue %s: %v", path, err)
				} else {
					rescue[index] = reader
				}
			}
		}
		defer func() {
			for _, reader := range rescue {
				if reader != nil {
					_ = reader.Close()
				}
			}
		}()
		defer func() {
			for _, file := range files {
				if file != nil {
					_ = file.Close()
				}
			}
		}()
		deadline := time.NewTimer(3 * time.Second)
		defer deadline.Stop()
		for !received[0] || !received[1] {
			select {
			case value := <-ready:
				received[value.index] = true
				files[value.index] = value.file
				if value.err != nil {
					t.Errorf("private FIFO cleanup open %s: %v", paths[value.index], value.err)
				}
			case <-deadline.C:
				t.Error("private FIFO cleanup did not drain all writer openers within 3s")
				return
			}
		}
		for index, path := range paths {
			if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeNamedPipe != 0 {
				body := known
				if index == 1 {
					body = payload
				}
				if err = queryCatalogRestorePrivateFile(path, body); err != nil {
					t.Errorf("private FIFO cleanup restore %s: %v", path, err)
				}
			}
		}
		for _, done := range joined {
			select {
			case <-done:
			case <-deadline.C:
				t.Error("private FIFO opener goroutine did not join within 3s")
				return
			}
		}
	}()
	var sourceDone <-chan struct{}
	if source != nil {
		sourceDone = source.done
	}
	fail := func(stage string, err error) {
		if source != nil {
			t.Fatalf("Source setup/guard %s: %v %s", stage, err, source.raw())
		}
		t.Fatalf("native cold FIFO %s: %v", stage, err)
	}
	accept := func(value opened) {
		received[value.index] = true
		files[value.index] = value.file
		if value.err != nil {
			fail("writer open", value.err)
		}
	}
	next := func() {
		select {
		case value := <-ready:
			accept(value)
		case <-sourceDone:
			fail("exited before FIFO rendezvous", source.failure)
		case <-ctx.Done():
			fail("reader rendezvous timeout", ctx.Err())
		}
	}
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	parallel := true
	for files[0] == nil || files[1] == nil {
		select {
		case value := <-ready:
			accept(value)
		case <-deadline.C:
			parallel = false
			goto release
		case <-sourceDone:
			fail("exited before FIFO rendezvous", source.failure)
		case <-ctx.Done():
			fail("reader entry timeout", ctx.Err())
		}
	}
release:
	// Preserve the original observation window and plugin-first release. On
	// old-native business RED the late local producer still completes and joins.
	for files[0] == nil {
		next()
	}
	// Original marketplace discovery reopens this same path. Keep the first
	// captured FIFO read held, but make subsequent actual opens see real bytes.
	if err := queryCatalogRestorePrivateFile(pluginPath, known); err != nil {
		fail("plugin path restore", err)
	}
	if _, err := files[0].Write(known); err != nil {
		fail("plugin write", err)
	}
	if err := files[0].Close(); err != nil {
		fail("plugin close", err)
	}
	for files[1] == nil {
		next()
	}
	if err := queryCatalogRestorePrivateFile(skillPath, payload); err != nil {
		fail("skill path restore", err)
	}
	if _, err := files[1].Write(payload); err != nil {
		fail("skill write", err)
	}
	if err := files[1].Close(); err != nil {
		fail("skill close", err)
	}
	return parallel
}

// The target has already been validated as this fixture's private FIFO. Atomic
// replacement preserves its captured open descriptor and supplies identical real
// bytes to later original readFile calls; it does not replace an IO primitive.
func queryCatalogRestorePrivateFile(path string, body []byte) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".actual-query-catalog-release-")
	if err != nil {
		return err
	}
	name := file.Name()
	defer os.Remove(name)
	if _, err = file.Write(body); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Ordinary startup has already completed getCommands and cache-only plugins;
// the original QueryEngine still creates its first getSlash wrapper after Record.
func TestActualWarmQueryEngineFirstSlashAfterRecordUsesStartupProducers(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual pinned Source Bun required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	f := newNativePluginFixture(t)
	delete(f.n.Environment, "CLAUDE_CODE_SIMPLE")
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	f.write(t, filepath.Join(f.config, "settings.json"), `{"enabledPlugins":{"sample@mkt":false},"agentTeamsEnabled":false,"alwaysThinkingEnabled":false}`)
	skill := filepath.Join(f.cwd, ".claude", "skills", "held", "SKILL.md")
	f.write(t, skill, "---\ndescription: Actual warm first slash\n---\nActual startup skill.")
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" {
			t.Error("unowned model request", r.Method, r.URL.String())
			http.Error(w, "unowned", 403)
			return
		}
		body, err := io.ReadAll(r.Body)
		if err != nil || !bytes.Contains(body, []byte("ACTUAL_INIT_CATALOG_USER")) {
			t.Errorf("actual warm model body %v %s", err, body)
		}
		emitPlanFixture(w, int(posts.Add(1)), "", "ACTUAL_INIT_CATALOG_DONE")
	}))
	defer peer.Close()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-query-init-catalog.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = f.home
	command.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "CC_HAHA_QUERY_INIT_CATALOG_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-warm-catalog", "ANTHROPIC_BASE_URL=" + peer.URL, "CLAUDE_CODE_MAX_RETRIES=0", "CLAUDE_CODE_DISABLE_ATTACHMENTS=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "PATH=" + filepath.Join(f.home, "bin")}
	input, _ := json.Marshal(map[string]any{"home": f.home, "peer": peer.URL, "skill": skill, "warmOriginal": true})
	command.Stdin = bytes.NewReader(input)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("actual warm original QueryEngine %v %s %s", err, stdout.String(), stderr.String())
	}
	var source struct {
		Init                     protocol.JSONObject
		WarmOriginal, PluginSame bool
		SlashCalls               []struct{ AfterRecord, SamePromise bool }
		SameRecordPromise        []bool
		Blocked                  []string
	}
	if err := json.Unmarshal(stdout.Bytes(), &source); err != nil || len(source.Blocked) > 0 || !source.WarmOriginal || !source.PluginSame || len(source.SlashCalls) != 1 || !source.SlashCalls[0].AfterRecord || !source.SlashCalls[0].SamePromise || len(source.SameRecordPromise) == 0 || !bytes.Contains(source.Init.Get("skills"), []byte(`"held"`)) {
		t.Fatalf("actual warm first-wrapper contract %v %s %s", err, stdout.String(), stderr.String())
	}
	for _, same := range source.SameRecordPromise {
		if !same {
			t.Fatal("Source observer replaced original Record Promise")
		}
	}
	f.n.Environment["ANTHROPIC_BASE_URL"], f.n.Environment["ANTHROPIC_API_KEY"] = peer.URL, "offline-warm-catalog"
	f.n.Environment["CLAUDE_CODE_MAX_RETRIES"] = "0"
	var init protocol.JSONObject
	disabled := false
	m, err := NewMainPrint(MainPrintOptions{Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard, Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "private actual init catalog", Tools: []string{""}, Sources: DefaultState(), MaxTurns: 2, PermissionMode: "default"}, OnOutput: func(raw []byte) error {
		var value protocol.JSONObject
		if value.UnmarshalJSON(raw) == nil && value.String("type") == "system" && value.String("subtype") == "init" {
			init = value
		}
		return nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer closeCancel()
		_ = m.ShutdownSession(closeCtx)
	})
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	load := m.commandCatalogLoad()
	if _, err, ready := load.ready(); !ready || err != nil {
		t.Fatal("actual startup commands were not settled before Record", err, ready)
	}
	plugin := m.nativeQueryCatalog.loader.CachedProducer()
	if _, err, ready := plugin.Ready(); !ready || err != nil {
		t.Fatal("actual startup plugin receipt was not settled before Record", err, ready)
	}
	if len(m.nativeQueryCatalog.slashes) != 0 {
		t.Fatal("constructor prewarmed first QueryEngine slash wrapper")
	}
	if err = m.RunTurn(ctx, objMain(rawMain(map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": "ACTUAL_INIT_CATALOG_USER"}}))); err != nil {
		t.Fatal(err)
	}
	if len(m.nativeQueryCatalog.slashes) != 1 || m.nativeQueryCatalog.pluginSource != plugin || !bytes.Contains(init.Get("skills"), []byte(`"held"`)) || posts.Load() != 2 {
		t.Fatalf("native first init did not consume retained startup receipts: slash=%d plugins=%v init=%s posts=%d", len(m.nativeQueryCatalog.slashes), m.nativeQueryCatalog.pluginSource == plugin, rawMain(init), posts.Load())
	}
}
