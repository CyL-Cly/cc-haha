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
	"sync/atomic"
	"testing"
	"time"
)

func TestActualSourceInteractiveQueryRefreshesToolsAfterPhysicalTool(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	fakeKey := "private-query-refresh"
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), rawMain(map[string]any{"customApiKeyResponses": map[string]any{"approved": []string{fakeKey[len(fakeKey)-20:]}}}), 0600); err != nil {
		t.Fatal(err)
	}

	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		n := requests.Add(1)
		want := "FixtureWrite"
		if n == 2 {
			want = "FixtureAfterRefresh"
		}
		request := mainObj(body)
		if !bytes.Contains(request.Get("tools"), []byte(`"name":"`+want+`"`)) {
			t.Errorf("Source actual model HTTP%d missing %s: %s", n, want, request.Get("tools"))
		}
		if n == 1 {
			emitNativeTaskTool(w, "source-file-tool", "FixtureWrite", map[string]any{"file_path": "effect.txt", "content": "written exactly once"})
		} else {
			emitPlanFixture(w, int(n), "", "Complete after actual refresh")
		}
	}))
	defer peer.Close()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-query-tools-refresh.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CLAUDE_CODE_TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_QUERY_LOOP_FIXTURE=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_DISABLE_AUTO_MEMORY=1", "ANTHROPIC_BASE_URL=" + peer.URL, "ANTHROPIC_API_KEY=private-query-refresh", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]any{"home": home, "origin": peer.URL}))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatal("actual Source whole query", err, string(output))
	}
	var result struct {
		ObservationSource string
		Capabilities      struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &result); err != nil {
		t.Fatal(err, string(output))
	}
	var observation struct {
		Error      any
		Executions int
		File       string
		Effects    []struct{ Kind, Value string }
	}
	if err := json.Unmarshal([]byte(result.ObservationSource), &observation); err != nil {
		t.Fatal(err, string(output))
	}
	refresh := []string{}
	for _, effect := range observation.Effects {
		if effect.Kind == "refresh-tools" {
			refresh = append(refresh, effect.Value)
		}
	}
	if observation.Error != nil || observation.Executions != 1 || observation.File != "written exactly once" || len(refresh) != 1 || refresh[0] != observation.File || requests.Load() != 2 || len(result.Capabilities.Blocked) != 0 {
		t.Fatal("actual Source refresh phase/physical file/requests", string(output))
	}
}
