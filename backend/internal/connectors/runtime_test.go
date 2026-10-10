package connectors

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func pin(data []byte) string {
	digest := sha256.Sum256(data)
	return "sha256-" + hex.EncodeToString(digest[:])
}

func archiveMember(t *testing.T, name string, body []byte) []byte {
	t.Helper()
	var out bytes.Buffer
	gz := gzip.NewWriter(&out)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0700, Size: int64(len(body))}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestActualDownloadFallsBackAndChecksBytes(t *testing.T) {
	data := []byte("verified-native-archive")
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path == "/bad" {
			_, _ = w.Write([]byte("wrong bytes"))
			return
		}
		_, _ = w.Write(data)
	}))
	defer peer.Close()
	got, err := DownloadVerified(context.Background(), []string{peer.URL + "/bad", peer.URL + "/good"}, pin(data))
	if err != nil || !bytes.Equal(got, data) || calls.Load() != 2 {
		t.Fatalf("download/checksum fallback: %q %v calls=%d", got, err, calls.Load())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := DownloadVerified(ctx, []string{peer.URL + "/good"}, pin(data)); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled download reached network: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatal("cancelled request reached peer")
	}
}

func TestActualNativeInstallationCheckTamperAndOwnedRemoval(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fixture executable; native Windows adapter evidence remains separate")
	}
	root := t.TempDir()
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	// This test executes an integrity-pinned disposable binary and tar. It
	// deliberately has no real service account or third-party network access.
	binary := []byte("#!/bin/sh\nif [ \"$1\" = --version ]; then echo 'lark-cli 1.0.95'; exit; fi\nprintf '%s' '{\"identity\":\"user\",\"verified\":true,\"identities\":{\"user\":{\"userName\":\"Fixture account\"}}}'\n")
	archive := archiveMember(t, "lark-cli", binary)
	var downloads atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { downloads.Add(1); _, _ = w.Write(archive) }))
	defer peer.Close()
	r := DefaultRuntime("darwin", "arm64", map[string]string{"HOME": home, "USERPROFILE": home, "PATH": "/usr/bin:/bin"})
	r.Pins = func(protocol.JSONObject, string) (ArtifactPins, error) {
		return ArtifactPins{pin(archive), pin(binary)}, nil
	}
	r.Download = func(ctx context.Context, urls []string, integrity string) ([]byte, error) {
		if len(urls) != 2 || !strings.Contains(urls[0], "lark-cli-1.0.95-darwin-arm64.tar.gz") {
			t.Errorf("original release URLs missing: %v", urls)
		}
		return DownloadVerified(ctx, []string{peer.URL}, integrity)
	}
	def := record(field("id", "feishu"), field("version", "1.0.95"), field("platforms", []string{"darwin-arm64"}))
	adapter, err := NativeAdapter(def, filepath.Join(root, "connectors"), r, func() string { return "fixture" })
	if err != nil {
		t.Fatal(err)
	}
	// Whole-repository atomic coverage instruments all packages concurrently.
	// This bounds the fixture's several real processes without changing any
	// production download, extraction, authentication or command deadline.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	phases := []string{}
	i, err := adapter.Prepare(ctx, func(p string, _ *string) { phases = append(phases, p) })
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(phases, ",") != "downloading,extracting,verifying" {
		t.Fatalf("installation phases: %v", phases)
	}
	info, err := os.Stat(i.Command)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatalf("executable mode: %v %v", info, err)
	}
	checked, err := adapter.Check(ctx, i)
	if err != nil || !checked.Authenticated || checked.Verification != "remote" {
		t.Fatalf("actual status process: %+v %v", checked, err)
	}
	if _, err := adapter.Prepare(ctx, func(string, *string) {}); err != nil || downloads.Load() != 1 {
		t.Fatalf("verified existing runtime was not reused: %v %d", err, downloads.Load())
	}
	if err := os.WriteFile(i.Command, []byte("#!/bin/sh\necho unsafe\n"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Check(ctx, i); err == nil {
		t.Fatal("tampered runtime was executed")
	}
	accounts := filepath.Join(root, "connectors", "accounts", "feishu")
	if err := os.MkdirAll(accounts, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(accounts, "keep"), []byte("owned-test-account"), 0600); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(root, "connectors", "runtime", "other")
	if err := os.MkdirAll(other, 0700); err != nil {
		t.Fatal(err)
	}
	if err := adapter.Remove(ctx, i); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned runtime not removed")
	}
	for _, path := range []string{filepath.Join(accounts, "keep"), other} {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("removal touched accounts or another connector: %s %v", path, err)
		}
	}
}

func TestActualProcessCancellationKillsDescendantEffects(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process group fixture; native Windows process evidence remains separate")
	}
	root := t.TempDir()
	effect := filepath.Join(root, "late-effect")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	r := DefaultRuntime("darwin", "arm64", map[string]string{"HOME": root, "USERPROFILE": root, "PATH": "/usr/bin:/bin", "EFFECT": effect})
	_, err := r.Run(ctx, "/bin/sh", []string{"-c", `(sleep 0.3; echo leaked > "$EFFECT") & echo started; wait`}, RunOptions{Timeout: 2 * time.Second, OnOutput: func(s string) {
		if strings.Contains(s, "started") {
			cancel()
		}
	}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("process cancellation: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := os.Stat(effect); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("descendant survived cancellation and produced an effect: %v", err)
	}
	_, _, _, err = runCaptured(context.Background(), "/bin/sh", []string{"-c", "printf '%0200d' 0"}, RunOptions{Timeout: time.Second}, 64, map[string]string{"HOME": root, "PATH": "/usr/bin:/bin"})
	if err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("combined output limit not enforced: %v", err)
	}
}
