package application

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/term"
)

func TestActualCLIClipboardImageDeleteResubmitCanonicalCacheAndNextTurnPTY(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS clipboard process and Unix PTY; other native primitives have separate tests")
	}
	python, err := exec.LookPath("python3")
	if err != nil {
		t.Fatal(err)
	}
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	home := t.TempDir()
	var picture bytes.Buffer
	if err = png.Encode(&picture, image.NewNRGBA(image.Rect(0, 0, 12, 8))); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(home, "fixture.png"), picture.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(home, "bin")
	if err = os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	// Replace only the owned physical clipboard executable. The public runner,
	// default key resolver, pill/editor, Session and native Harness stay real.
	script := "#!" + python + "\n" + `import os,re,sys,pathlib
home=pathlib.Path(os.environ['HOME']).resolve()
args=' '.join(sys.argv[1:])
target=pathlib.Path(re.search(r'POSIX file "([^"]+)"',args).group(1))
assert target.parent.resolve().is_relative_to(home)
target.write_bytes((home/'fixture.png').read_bytes())
with (home/'clipboard-calls').open('a') as f:f.write('read\n')
`
	if err = os.WriteFile(filepath.Join(bin, "osascript"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, python, "testdata/terminal_media_pty.py", binary, home)
	cmd.Env = []string{"PATH=" + bin + ":/usr/bin:/bin", "HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "TERM=xterm-256color", "CC_HAHA_TERMINAL_MEDIA_PTY_HELPER=1"}
	capturePublicGoCoverage(t, cmd)
	output, err := cmd.CombinedOutput()
	if err != nil || !bytes.Contains(output, []byte(`"terminalRestored": true`)) {
		t.Fatalf("public media PTY %v %.6000s", err, output)
	}
}

func TestActualCLIClipboardImageFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_TERMINAL_MEDIA_PTY_HELPER") != "1" {
		t.Skip("owned private PTY helper")
	}
	home := os.Getenv("HOME")
	picture, err := os.ReadFile(filepath.Join(home, "fixture.png"))
	if err != nil {
		t.Fatal(err)
	}
	encoded := base64.StdEncoding.EncodeToString(picture)
	var calls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The original CLI init HEAD is distinct from model POSTs.
		if r.Method == http.MethodHead {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		body, _ := io.ReadAll(r.Body)
		n := calls.Add(1)
		if !bytes.Contains(body, []byte(encoded)) || !bytes.Contains(body, []byte("image/png")) {
			t.Errorf("actual clipboard bytes absent from model turn %d", n)
		}
		if n == 1 && bytes.Count(body, []byte(`"type":"image"`)) != 1 {
			t.Errorf("deleted pasted image reached actual request: %.2500s", obj(body).Get("messages"))
		}
		if n == 2 && !bytes.Contains(body, []byte("Continue after the pasted image.")) {
			t.Error("next normal input did not reach shared Harness")
		}
		if n > 2 {
			t.Errorf("clipboard/menu caused extra model request %d", n)
		}
		text := "MEDIA_PTY_IMAGE_DONE"
		if n == 2 {
			text = "MEDIA_PTY_NEXT_DONE"
		}
		response(w, text, "", nil, text)
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Stderr = os.Stderr
	if err = os.MkdirAll(o.Environment["CLAUDE_CONFIG_DIR"], 0700); err != nil {
		t.Fatal(err)
	}
	global := raw(map[string]any{"hasCompletedOnboarding": true, "projects": map[string]any{o.WorkDir: map[string]any{"hasTrustDialogAccepted": true}}})
	if err = os.WriteFile(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], ".claude.json"), global, 0600); err != nil {
		t.Fatal(err)
	}
	before, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 28*time.Second)
	defer cancel()
	code, err := RunCommand(ctx, Command{Arguments: o.Arguments}, o, os.Stdin, os.Stdout, os.Environ())
	if err != nil || code != 0 {
		t.Fatal(code, err)
	}
	// Settle XNU's pending canonical input through the same physical probe used
	// by other public PTY consumers; do not mask or change any termios bits.
	if _, err = nativeTUIPresentationTerminalProbe(os.Stdin.Fd()); err != nil {
		t.Fatal(err)
	}
	after, err := term.GetState(os.Stdin.Fd())
	if err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "projects", "*", "*.jsonl"))
	if err != nil || len(files) != 1 {
		t.Fatal("canonical transcript", files, err)
	}
	disk, err := os.ReadFile(files[0])
	if err != nil || !bytes.Contains(disk, []byte(`"imagePasteIds":[2]`)) || bytes.Contains(disk, []byte(`"imagePasteIds":[1]`)) {
		t.Fatal("deleted/current canonical paste identity", err)
	}
	for _, line := range bytes.Split(disk, []byte{'\n'}) {
		if bytes.Contains(line, []byte("Continue after the pasted image.")) && bytes.Contains(line, []byte("imagePasteIds")) {
			t.Fatal("display paste identity leaked into next canonical message")
		}
	}
	caches, err := filepath.Glob(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "image-cache", "*", "2.png"))
	if err != nil || len(caches) != 1 {
		t.Fatal("actual cache", caches, err)
	}
	cached, err := os.ReadFile(caches[0])
	if err != nil || !bytes.Equal(cached, picture) {
		t.Fatal("original clipboard cache bytes changed", err)
	}
	reads, err := os.ReadFile(filepath.Join(home, "clipboard-calls"))
	if err != nil || string(reads) != "read\nread\n" {
		t.Fatal("actual owned clipboard subprocess reads", err, string(reads))
	}
	receipt, _ := json.Marshal(map[string]any{"terminalRestored": reflect.DeepEqual(before, after), "requests": calls.Load(), "originalCacheBytes": true, "deletedImageAbsent": true, "nextIdentityClear": true})
	if err = os.WriteFile(filepath.Join(home, "media-pty-receipt.json"), receipt, 0600); err != nil {
		t.Fatal(err)
	}
	for {
		if _, err = os.Stat(filepath.Join(home, "release-media-child")); err == nil {
			break
		}
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
		time.Sleep(10 * time.Millisecond)
	}
	if calls.Load() != 2 || !reflect.DeepEqual(before, after) {
		t.Fatal("actual terminal/model receipt", calls.Load())
	}
}
