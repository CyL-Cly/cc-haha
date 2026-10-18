package application

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/ansi"
)

func TestActualSourceUsageLimitAndExtraSections(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("pinned actual Source required")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	credentials := map[string]any{"claudeAiOauth": map[string]any{"accessToken": "fake-usage-token", "scopes": []string{"user:inference", "user:profile"}, "expiresAt": nil, "subscriptionType": "max"}}
	if err := os.WriteFile(filepath.Join(config, ".credentials.json"), raw(credentials), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(config, ".claude.json"), raw(map[string]any{"oauthAccount": map[string]any{"billingType": "stripe_subscription"}}), 0600); err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`{}`)) }))
	defer peer.Close()
	type renderCase struct {
		name  string
		row   map[string]any
		data  map[string]any
		width int
	}
	cases := []renderCase{}
	for _, width := range []int{80, 40} {
		for _, percent := range []float64{0, 23.9, 100, 130} {
			limit := map[string]any{"utilization": percent, "resets_at": "2026-10-14T05:00:00Z"}
			cases = append(cases, renderCase{name: "limit", width: width, row: map[string]any{"title": "Current session", "limit": limit, "width": width}, data: map[string]any{"five_hour": limit}})
		}
	}
	for _, extra := range []map[string]any{{"is_enabled": false, "monthly_limit": 10000, "used_credits": 0, "utilization": 0}, {"is_enabled": true, "monthly_limit": nil, "used_credits": nil, "utilization": nil}, {"is_enabled": true, "monthly_limit": 10000, "used_credits": 2390, "utilization": 23.9}} {
		for _, width := range []int{80, 40} {
			cases = append(cases, renderCase{name: "extra", width: width, row: map[string]any{"extra": extra, "width": width}, data: map[string]any{"extra_usage": extra}})
		}
	}
	rows := []any{}
	for _, row := range cases {
		rows = append(rows, row.row)
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-terminal-usage.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "TZ=UTC", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TERMINAL_USAGE_FIXTURE=1"}
	cmd.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "peer": peer.URL, "now": "2026-10-14T00:00:00Z", "render": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct {
		Renders []string
		Caps    struct{ Blocked []string }
	}
	if err = json.Unmarshal(output, &source); err != nil || len(source.Renders) != len(cases) || len(source.Caps.Blocked) > 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range cases {
		t.Run(row.name+"-"+string(raw(i)), func(t *testing.T) {
			value := map[string]any{"utilization": row.data, "subscriptionType": "max", "extraUsageCommandEnabled": true, "now": "2026-10-14T00:00:00Z", "timezone": "UTC"}
			lines := tui.UsageVisualization(raw(map[string]any{"value": value}), row.width+2)
			lines = lines[:len(lines)-2]
			if row.name == "extra" {
				lines = lines[2:]
			}
			for j, line := range lines {
				lines[j] = strings.TrimRight(line, " ")
			}
			got := strings.Join(lines, "\n")
			want := strings.TrimRight(ansi.Strip(source.Renders[i]), "\n")
			if got != want {
				t.Fatalf("actual original section\nGo:\n%s\nSource:\n%s", got, want)
			}
		})
	}
}
