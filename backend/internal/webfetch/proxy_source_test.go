package webfetch

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestAxiosProxyAndPrimitiveHelpersMatchActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	home := t.TempDir()
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0700)
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	rows := []map[string]any{}
	for _, endpoint := range []string{"https://sub.example.org/path", "https://example.org:443/path", "http://example.org/path", "https://[::1]:444/path"} {
		for _, noProxy := range []string{"", "*", ".example.org", "*.example.org", "example.org:443", "example.org:80", "[::1]:444", "EXAMPLE.ORG", "other, example.org"} {
			rows = append(rows, map[string]any{"url": endpoint, "environment": map[string]string{"https_proxy": "https://proxy-a:80", "HTTP_PROXY": "http://proxy-b:80", "ALL_PROXY": "proxy-c:81", "NO_PROXY": noProxy}})
		}
	}
	rows = append(rows, map[string]any{"url": "https://example.org", "environment": map[string]string{"HTTPS_PROXY": "proxy-no-scheme:8080"}})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-webfetch-markdown.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "CC_HAHA_WEBFETCH_MARKDOWN_FIXTURE=1", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(map[string]any{"homeDir": home, "html": []string{}, "proxyRows": rows, "randoms": []float64{0, 0.5, 0.1, 0.123456789, 0.999999999}})
	cmd.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, e := cmd.Output()
	if e != nil {
		t.Fatalf("Source %v %.1800s", e, stderr.String())
	}
	var source struct {
		Proxies, Randoms []string
		Capabilities     struct{ Blocked []string }
	}
	if json.Unmarshal(out, &source) != nil || len(source.Proxies) != len(rows) || len(source.Capabilities.Blocked) > 0 {
		t.Fatalf("Source %.2000s", out)
	}
	for i, row := range rows {
		got := AxiosProxyURL(row["url"].(string), row["environment"].(map[string]string))
		if got != source.Proxies[i] {
			t.Errorf("proxy%d Go%q Source%q", i, got, source.Proxies[i])
		}
	}
	for i, value := range []float64{0, 0.5, 0.1, 0.123456789, 0.999999999} {
		if got := RandomBase36Suffix(value); got != source.Randoms[i] {
			t.Errorf("random%v Go%q Source%q", value, got, source.Randoms[i])
		}
	}
}
