package clibootstrap

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
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestCLIStartupFilesActualSourceParserPathsBearerAndDownloadedBytes(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	f := newNativePluginFixture(t)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer source-ingress-token" || r.Header.Get("anthropic-version") != "2023-06-01" || r.Header.Get("anthropic-beta") != "files-api-2025-04-14,oauth-2025-04-20" {
			t.Error("Source file ingress headers changed", r.Header)
		}
		if strings.Contains(r.URL.Path, "file_missing") {
			w.WriteHeader(404)
			return
		}
		_, _ = fmt.Fprint(w, "SOURCE_PHYSICAL_FILE_BYTES")
	}))
	defer peer.Close()
	specs := []string{"file_one:docs/one.txt file_two:two:colon.txt", "invalid", ":empty", "empty:", "file_missing:missing.txt", "file_bad:../outside"}
	paths := []string{"docs/../a.txt", "../outside", "..name", "/uploads/a.txt", filepath.Join(f.cwd, "source-session", "uploads", "a.txt")}
	input := map[string]any{"home": f.home, "cwd": f.cwd, "sid": "source-session", "base": peer.URL + "/gateway/v1/", "specs": specs, "paths": paths}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-cli-startup-files.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	child := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	child.Dir = f.home
	child.Env = []string{"HOME=" + f.home, "USERPROFILE=" + f.home, "CLAUDE_CONFIG_DIR=" + f.config, "TMPDIR=" + f.n.Temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(f.home), "BUN_OPTIONS=--no-env-file", "CC_HAHA_CLI_FILE_FIXTURE=1", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "NODE_ENV=production", "ANTHROPIC_API_KEY=fake-source-files-key"}
	child.Stdin = bytes.NewReader(rawMain(input))
	out, err := child.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var source struct {
		Files []struct {
			FileID       string `json:"fileId"`
			RelativePath string `json:"relativePath"`
		}
		Paths   []*string
		Results []struct {
			FileID       string `json:"fileId"`
			Path         string
			Success      bool
			Error        string
			BytesWritten int
		}
		Blocked []string
	}
	if err := json.Unmarshal(out, &source); err != nil || len(source.Blocked) > 0 {
		t.Fatal(err, string(out))
	}
	files := parseStartupFileSpecs(specs)
	if len(files) != len(source.Files) {
		t.Fatal(files, source.Files)
	}
	for i, file := range files {
		if file.fileID != source.Files[i].FileID || file.relativePath != source.Files[i].RelativePath {
			t.Fatal(file, source.Files[i])
		}
	}
	for i, input := range paths {
		actual := startupFilePath(f.cwd, "source-session", input)
		want := ""
		if source.Paths[i] != nil {
			want = *source.Paths[i]
		}
		if actual != want {
			t.Fatal("Source path diverged", input, actual, want)
		}
	}
	// Download against the same actual HTTP API into another isolated SID,
	// while retaining the Source byte contents for a physical comparison.
	job := beginStartupFiles(f.n, files, "go-session", peer.URL+"/gateway/v1/", "source-ingress-token", peer.Client(), startupFileWait)
	m := &MainPrint{startupFiles: job}
	defer m.closeCLIStartupFiles()
	if err := m.AwaitCLIStartupFiles(ctx); err != nil {
		t.Fatal(err)
	}
	for i, result := range job.results {
		want := source.Results[i]
		if result.fileID != want.FileID || (result.err == nil) != want.Success {
			t.Fatal("Source download result diverged", result, want)
		}
		if result.err != nil {
			if result.err.Error() != want.Error {
				t.Fatal("Source file error diverged", result.err, want.Error)
			}
			continue
		}
		actualBytes, actualErr := os.ReadFile(result.path)
		wantBytes, wantErr := os.ReadFile(want.Path)
		if actualErr != nil || wantErr != nil || !reflect.DeepEqual(actualBytes, wantBytes) || result.bytes != want.BytesWritten {
			t.Fatal("actual Source saved file bytes diverged", actualErr, wantErr, result, want)
		}
	}
}
