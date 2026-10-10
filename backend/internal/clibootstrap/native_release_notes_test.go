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
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

func TestActualReleaseNotesSourceLocalMessagesNoPOSTCanonicalAndNextHarness(t *testing.T) {
	f := worktreeMainFixture(t)
	f.write(t, filepath.Join(f.config, "settings.json"), `{"agentTeamsEnabled":false,"alwaysThinkingEnabled":false,"enabledPlugins":{"sample@mkt":false}}`)
	f.n.Environment["CLAUDE_CODE_DISABLE_ATTACHMENTS"] = "1"
	cached := "## 2.0.0\n- PRIVATE_RELEASE_TWO_784\n## 1.0.0\n- PRIVATE_RELEASE_ONE_784\n"
	f.write(t, filepath.Join(f.config, "cache", "changelog.md"), cached)
	var posts atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		posts.Add(1)
		if !bytes.Contains(body, []byte("PRIVATE_RELEASE_ONE_784")) || !bytes.Contains(body, []byte("<local-command-stdout>")) {
			t.Error("real local command canonical history absent")
		}
		emitWorktreeModel(w, 1, "", "", "RELEASE_SHARED_HARNESS_NEXT_784")
	}))
	defer peer.Close()
	f.n.Environment["ANTHROPIC_BASE_URL"] = peer.URL
	disabled := false
	var stdout, diagnostics bytes.Buffer
	m, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", SystemPrompt: "actual local release and next Harness", Tools: []string{"Read"}, MaxTurns: 2, Thinking: requestcontext.Thinking{Type: "disabled"}, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: &stdout, Error: &diagnostics})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = m.ShutdownSession(context.Background()) })
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Second)
	defer cancel()
	if err = m.StartSession(ctx); err != nil {
		t.Fatal(err)
	}
	user := func(text string) protocol.JSONObject {
		return protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("user")}, protocol.JSONField{Name: "message", Value: rawMain(map[string]string{"role": "user", "content": text})})
	}
	slash := "/release-notes ignored input"
	if err = m.RunTurn(ctx, user(slash)); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if posts.Load() != 0 {
		t.Fatal("local release invoked model")
	}
	var actual []protocol.JSONObject
	if err = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		history, e := m.Host.OriginalHistoryOnOwner(scope)
		if e != nil {
			return e
		}
		for _, message := range history {
			record := message.Record.Clone()
			record.Delete("uuid")
			record.Delete("timestamp")
			actual = append(actual, record)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if bun := os.Getenv("CC_HAHA_SOURCE_BUN"); bun != "" {
		sourceHome := t.TempDir()
		fixture, _ := filepath.Abs("../releasenotes/testdata/release-cache.fixture.ts")
		cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
		cmd.Dir = sourceHome
		cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_RELEASE_CACHE_FIXTURE=1", "NODE_ENV=production", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
		data, _ := json.Marshal(map[string]any{"home": sourceHome, "url": peer.URL, "cached": cached, "nonInteractive": true, "essential": "1", "slash": slash})
		cmd.Stdin = bytes.NewReader(data)
		out, e := cmd.CombinedOutput()
		if e != nil {
			t.Fatal(e, string(out))
		}
		var source struct {
			SlashResult struct {
				Messages    []protocol.JSONObject
				ShouldQuery bool
				ResultText  string
			}
		}
		if e = json.Unmarshal(out, &source); e != nil {
			t.Fatal(e, string(out))
		}
		if source.SlashResult.ShouldQuery || !planSourcesEqual(rawMain(actual), rawMain(source.SlashResult.Messages)) {
			t.Fatalf("local Source mismatch Go%s Source%s", rawMain(actual), out)
		}
	} else {
		t.Log("actual Source local message comparison not run; Bun unavailable")
	}
	if err = m.RunTurn(ctx, user("Continue after local release notes.")); err != nil {
		t.Fatal(err, diagnostics.String())
	}
	if posts.Load() != 1 {
		t.Fatal("shared Harness next turn", posts.Load())
	}
	if err = m.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	files, _ := filepath.Glob(filepath.Join(f.config, "projects", "*", "*.jsonl"))
	if len(files) != 1 {
		t.Fatal(files)
	}
	disk, e := os.ReadFile(files[0])
	if e != nil || !bytes.Contains(disk, []byte("RELEASE_SHARED_HARNESS_NEXT_784")) || !bytes.Contains(disk, []byte("<command-name>/release-notes")) {
		t.Fatal("physical canonical command result missing", e)
	}
	if bytes.Count(disk, []byte(`"type":"assistant"`)) != 1 {
		t.Fatal(fmt.Errorf("local display assistant must not be canonical: %s", disk))
	}
}
