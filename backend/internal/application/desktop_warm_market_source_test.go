package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceMarketClearsWarmSkillCacheWhileMissingRootDetectorHasNoSignal(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	const content = "---\nname: warm-market-skill\ndescription: Actual warm desktop installation\n---\nWARM_INSTALLED_INSTRUCTIONS $ARGUMENTS\n"
	market := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/api/v1/skills/warm-market-skill/file":
			fmt.Fprint(w, content)
		case r.URL.Path == "/api/v1/skills/warm-market-skill/versions/1.0":
			json.NewEncoder(w).Encode(map[string]any{"version": map[string]any{"version": "1.0", "files": []any{map[string]any{"path": "SKILL.md", "size": len(content)}}}})
		case r.URL.Path == "/api/v1/skills/warm-market-skill":
			json.NewEncoder(w).Encode(map[string]any{"skill": map[string]any{"slug": "warm-market-skill", "displayName": "Warm Market Skill", "description": "Actual warm installation"}, "latestVersion": map[string]any{"version": "1.0"}})
		default:
			t.Errorf("unexpected actual Source market request %s", r.URL.Path)
			http.Error(w, "missing", 404)
		}
	}))
	defer market.Close()
	root := t.TempDir()
	home := filepath.Join(root, "home")
	config := filepath.Join(home, ".claude")
	cwd := filepath.Join(home, "work")
	temp := filepath.Join(home, "tmp")
	for _, p := range []string{config, cwd, temp, filepath.Join(home, "managed")} {
		if err := os.MkdirAll(p, 0700); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture, _ := filepath.Abs("../../testdata/native-warm-market-skill.fixture.ts")
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + root, "CC_HAHA_WARM_MARKET_SKILL_FIXTURE=1", "NODE_ENV=production", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "BUN_OPTIONS=--no-env-file", "HAHA_MARKET_BASE_CLAWHUB=" + market.URL, "HAHA_MARKET_BASE_SKILLHUB=" + market.URL, "ANTHROPIC_API_KEY=fake-loopback-only", "ANTHROPIC_BASE_URL=http://127.0.0.1:1", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(raw(map[string]string{"cwd": cwd}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatalf("actual Source %v %s", err, stderr.Bytes())
	}
	var result struct {
		Before, After, Removed              []string
		InitialMissingRoot, LaterRootExists bool
		DetectorNotifications               int
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Fatalf("actual Source output %s: %v", out, err)
	}
	if len(result.Before) != 0 || len(result.After) != 1 || result.After[0] != "warm-market-skill" || len(result.Removed) != 0 || !result.InitialMissingRoot || !result.LaterRootExists || result.DetectorNotifications != 0 {
		t.Fatalf("actual frozen market producer/detector semantics changed: %s", out)
	}
}
