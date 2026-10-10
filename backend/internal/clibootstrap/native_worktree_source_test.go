package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
)

func TestWorktreeRuntimeActualSourceToolAndMetadataLifecycle(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("requires explicit actual Source Bun")
	}
	home := t.TempDir()
	home, _ = filepath.EvalSymlinks(home)
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-worktree-runtime.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_WORKTREE_RUNTIME_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source runtime %v %.3500s", e, stderr.String())
	}
	var source struct {
		Enter, Keep struct{ Data protocol.JSONObject }
		Denied      struct {
			Result    bool
			Message   string
			ErrorCode int
		}
		AfterEnter, AfterExit struct {
			Cwd, Original, Project, State string
			Current                       json.RawMessage
		}
		OriginalHead string
		Root         string
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(output, &source); e != nil || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source isolation %v %.3000s", e, output)
	}
	f := worktreeMainFixture(t)
	m := worktreeOnlyMain(t, f)
	// Use the real MainPrint startup and writer, with an explicit existing local
	// transcript pointer like Source adoptResumedSessionFile. No inference occurs.
	var enter, keep protocol.JSONObject
	var firstState, lastState []byte
	var path string
	var tools []queryloop.RegisteredTool
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		m.Controller.Writer().AdoptSessionFile()
		path = m.Controller.Writer().Snapshot().String("path")
		if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
			return e
		}
		if e = os.WriteFile(path, nil, 0644); e != nil {
			return e
		}
		_, tools, e = m.nativeTools.Catalog(scope)
		return e
	}); e != nil {
		t.Fatal(e)
	}
	find := func(name string) queryloop.RegisteredTool {
		for _, tool := range tools {
			if tool.Name == name {
				return tool
			}
		}
		return queryloop.RegisteredTool{}
	}
	call := func(name, input string) protocol.JSONObject {
		var out protocol.JSONObject
		e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			var in protocol.JSONObject
			_ = in.UnmarshalJSON([]byte(input))
			tool := find(name)
			if tool.StartCallJSON == nil {
				return &builtinwrite.ValidationError{Message: "actual worktree registry absent", ErrorCode: 1}
			}
			future, e := tool.StartCallJSON(ctx, in, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}})
			if e != nil {
				return e
			}
			result, e := future.Await(scope)
			if e != nil {
				return e
			}
			return out.UnmarshalJSON(result.Data)
		})
		if e != nil {
			t.Fatal(name, e)
		}
		return out
	}
	enter = call("EnterWorktree", `{"name":"private/feature"}`)
	firstState, e = os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}
	current := m.state.Snapshot()
	normalizeState := func(s string) string { return strings.ReplaceAll(s, source.Root, f.cwd) }
	if current.CWD != normalizeState(source.AfterEnter.Cwd) || current.OriginalCWD != normalizeState(source.AfterEnter.Original) || current.ProjectRoot != normalizeState(source.AfterEnter.Project) {
		t.Errorf("Source enter identity differs %+v %+v", source.AfterEnter, current)
	}
	if e = os.WriteFile(filepath.Join(current.CWD, "proof.txt"), []byte("WORKTREE_PRIVATE_OK"), 0600); e != nil {
		t.Fatal(e)
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var remove protocol.JSONObject
		_ = remove.UnmarshalJSON([]byte(`{"action":"remove"}`))
		failure := find("ExitWorktree").ValidateInput(ctx, remove, &builtinwrite.CallContext{Scope: scope})
		if value, ok := failure.(*builtinwrite.ValidationError); !ok || value.ErrorCode != source.Denied.ErrorCode || value.Message != normalizeState(source.Denied.Message) || source.Denied.Result {
			t.Errorf("Source refusal differs Go%v Source%+v", failure, source.Denied)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	keep = call("ExitWorktree", `{"action":"keep"}`)
	lastState, e = os.ReadFile(path)
	if e != nil {
		t.Fatal(e)
	}

	normalize := func(data []byte, root string) string { return strings.ReplaceAll(string(data), root, "/__repo__") }
	for _, pair := range [][2]protocol.JSONObject{{source.Enter.Data, enter}, {source.Keep.Data, keep}} {
		a, _ := pair[0].MarshalJSON()
		b, _ := pair[1].MarshalJSON()
		if normalize(a, source.Root) != normalize(b, f.cwd) {
			t.Errorf("actual Tool results Source%s Go%s", a, b)
		}
	}
	// The SHA is repository content, not a transport constant; compare it to
	// each real repository's HEAD instead of normalizing it into fixed data.
	var nativeEntry, sourceEntry protocol.JSONObject
	_ = nativeEntry.UnmarshalJSON(bytes.TrimSpace(firstState))
	_ = sourceEntry.UnmarshalJSON([]byte(strings.TrimSpace(source.AfterEnter.State)))
	gitHead, e := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: "/usr/bin/git", Arguments: []string{"rev-parse", "HEAD"}, CWD: f.cwd, Environment: f.n.Environment})
	if e != nil || gitHead.Code != 0 {
		t.Fatal("private physical HEAD", gitHead, e)
	}
	for i, pair := range []*protocol.JSONObject{&nativeEntry, &sourceEntry} {
		var state protocol.JSONObject
		_ = state.UnmarshalJSON(pair.Get("worktreeSession"))
		want := source.OriginalHead
		if i == 0 {
			want = strings.TrimSpace(gitHead.Stdout)
		}
		if head := state.String("originalHeadCommit"); len(head) != 40 || head != want {
			t.Errorf("real HEAD metadata %s want%s", head, want)
		}
		state.Delete("originalHeadCommit")
		value, _ := state.MarshalJSON()
		pair.Set("worktreeSession", value)
	}

	a, _ := sourceEntry.MarshalJSON()
	b, _ := nativeEntry.MarshalJSON()
	if normalize(a, source.Root) != normalize(b, f.cwd) {
		t.Errorf("actual eager metadata Source%s Go%s", a, b)
	}
	if bytes.Count(lastState, []byte(`"type":"worktree-state"`)) != 2 || !bytes.HasSuffix(bytes.TrimSpace(lastState), []byte(`"worktreeSession":null,"sessionId":"11111111-1111-4111-8111-111111111111"}`)) {
		t.Fatalf("single eager entry lifecycle %s", lastState)
	}
	if value, e := os.Stat(path); e != nil || value.Mode().Perm() != 0600 {
		t.Fatal("original descriptor mode", value, e)
	}
	if _, e := os.Stat(worktree.PathFor(f.cwd, "private/feature")); e != nil {
		t.Fatal("keep did not preserve actual directory", e)
	}
}
