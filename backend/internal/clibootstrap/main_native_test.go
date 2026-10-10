package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
)

func nativeForTest(t *testing.T) *NativeBoundary {
	t.Helper()
	root, e := os.MkdirTemp("", "cc-haha-native-control-")
	if e != nil {
		t.Fatal(e)
	}
	root, e = filepath.EvalSymlinks(root)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	for _, s := range []string{"home", "workspace", "tmp"} {
		os.MkdirAll(filepath.Join(root, s), 0700)
	}
	env := map[string]string{"HOME": filepath.Join(root, "home"), "USERPROFILE": filepath.Join(root, "home"), "CLAUDE_CONFIG_DIR": filepath.Join(root, "home", ".claude"), "TMPDIR": filepath.Join(root, "tmp"), "ANTHROPIC_BASE_URL": "http://127.0.0.1:9", "ANTHROPIC_API_KEY": "loopback-cli-tool-restart-no-quota-key", "NODE_ENV": "production", "USER_TYPE": "external", "TZ": "UTC", "BUN_OPTIONS": "--no-env-file", "USE_BUILTIN_RIPGREP": "false", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
	n, e := NewOfflineNativeBoundary(env, filepath.Join(root, "workspace"))
	if e != nil {
		t.Fatal(e)
	}
	// This fixture already declares external policy absent. Keep the native
	// notifier's process cache explicit when a test promotes it to NativeProcess.
	n.mdm = &nativeMDMCache{read: func(context.Context) (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }}
	return n
}
func TestNativeGuardianRootsSymlinkDescriptorAndExternalSnapshots(t *testing.T) {
	n := nativeForTest(t)
	for _, r := range []string{n.Home, n.CWD, n.Temp} {
		p := filepath.Join(r, "positive")
		if e := n.check("positive", p); e != nil {
			t.Fatal(e)
		}
		os.WriteFile(p, []byte("private"), 0600)
		if b, e := n.readFile(p); e != nil || string(b) != "private" {
			t.Fatal(e)
		}
	}
	outside := filepath.Join(filepath.Dir(n.Home), "outside")
	os.WriteFile(outside, []byte("never read"), 0600)
	if _, e := n.readFile(outside); e == nil {
		t.Fatal("outside allocation directory admitted")
	}
	link := filepath.Join(n.Home, "link")
	os.Symlink(outside, link)
	if _, e := n.readFile(link); e == nil {
		t.Fatal("symlink admitted")
	}
	p := filepath.Join(n.Home, "positive")
	h, e := n.openRead(p)
	if e != nil {
		t.Fatal(e)
	}
	defer h.Close()
	os.Rename(p, p+".old")
	os.WriteFile(p, []byte("replacement"), 0600)
	if _, e = h.ReadAt(make([]byte, 7), 0); e == nil {
		t.Fatal("descriptor identity change admitted")
	}
	if _, e = n.readFile(filepath.Join(n.ManagedRoot, "CLAUDE.md")); e == nil || errors.Is(e, fs.ErrNotExist) {
		t.Fatal("managed generic denial became ENOENT", e)
	}
	if _, e = n.stat(filepath.Join(filepath.Dir(n.CWD), ".git")); !errors.Is(e, fs.ErrNotExist) {
		t.Fatal("frozen external git absence", e)
	}
	n.Environment["ANTHROPIC_BASE_URL"] = "https://api.anthropic.com"
	if n.TransportOrigin() == nil {
		t.Fatal("publicnetwork admitted")
	}
	n.Environment["ANTHROPIC_BASE_URL"] = "http://127.0.0.1:9"
	n.Environment["ANTHROPIC_API_KEY"] = "real-looking"
	if n.TransportOrigin() == nil {
		t.Fatal("nonfixture credential admitted")
	}
}
func TestMainPrintFactoryBuildsActualSharedGraphAndOwnGlobalID(t *testing.T) {
	n := nativeForTest(t)
	args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	if p.Controller == nil || p.Registry == nil || p.Startup == nil || p.Owner == nil || p.Global == nil {
		t.Fatal("factory did not construct services")
	}
	g, e := p.Global.Get()
	if e != nil || len(g.String("userID")) != 64 {
		t.Fatal("actual Source getOrCreateUserID equivalent missing", e, g)
	}
	if out.Len() != 0 || p.Host != nil {
		t.Fatal("constructor performed model business")
	}
}

func TestPublicMainGraphEOFHasExactlyOneSDKHostBorrower(t *testing.T) {
	n := nativeForTest(t)
	args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	binding, e := p.Controller.BorrowNativeCopyBinding(p.Owner)
	if e != nil || binding == nil {
		t.Fatal(e)
	}
	code, e := p.Run(context.Background())
	if e != nil || code != 0 || p.Host == nil {
		t.Fatal("actual shared public graph did not reach EOF/cleanup", code, e, errOut.String())
	}
	if out.Len() != 0 {
		t.Fatal("EOF performed ASK/model business", out.String())
	}
	if _, e = p.Controller.Writer().PendingWriter(p.Owner); e == nil {
		t.Fatal("second SDK Host borrower guard was weakened")
	}
}

func TestPublicMainHooksUseActualEnabledSourceMerge(t *testing.T) {
	n := nativeForTest(t)
	if e := os.MkdirAll(filepath.Join(n.Home, ".claude"), 0700); e != nil {
		t.Fatal(e)
	}
	// This valid active setting belongs to a disabled file source. Source's
	// merged startup excludes it when --setting-sources is the empty string.
	if e := os.WriteFile(filepath.Join(n.Home, ".claude/settings.json"), []byte(`{"hooks":{"SessionStart":[{"hooks":[{"type":"command","command":"not-executed"}]}]}}`), 0600); e != nil {
		t.Fatal(e)
	}
	args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	code, e := p.Run(context.Background())
	if e != nil || code != 0 {
		t.Fatal("disabled settings source affected actual public hooks", code, e)
	}
	if out.Len() != 0 {
		t.Fatal("EOF unexpectedly performed model business")
	}
}

func TestPublicMainPreservesExistingRuntimeEntrypoint(t *testing.T) {
	for _, value := range []string{"sdk", "cli", "unknown"} {
		t.Run(value, func(t *testing.T) {
			n := nativeForTest(t)
			n.Environment["CLAUDE_CODE_ENTRYPOINT"] = value
			args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
			if e != nil {
				t.Fatal(e)
			}
			var out, errOut bytes.Buffer
			p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: bytes.NewReader(nil), Output: &out, Error: &errOut})
			if p != nil {
				defer p.Close()
			}
			if e == nil || n.Environment["CLAUDE_CODE_ENTRYPOINT"] != value {
				t.Fatal("unported existing entrypoint overwritten or admitted", value, e, n.Environment["CLAUDE_CODE_ENTRYPOINT"])
			}
		})
	}
}
