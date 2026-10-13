package clibootstrap

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"
)

// NewOfflineAgentsNativeBoundary is a separate OS fixture admission for a
// read-only subcommand. It does not change print's transport/tool guard.
func NewOfflineAgentsNativeBoundary(env map[string]string, cwd string) (*NativeBoundary, error) {
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		return nil, mainCap("agents offline native platform")
	}
	home := env["HOME"]
	root := filepath.Dir(home)
	tmp := filepath.Join(root, "tmp")
	if !filepath.IsAbs(root) || !strings.HasPrefix(filepath.Base(root), "cc-haha-") || filepath.Base(home) != "home" || cwd != filepath.Join(root, "workspace") || env["USERPROFILE"] != home || env["CLAUDE_CONFIG_DIR"] != filepath.Join(home, ".claude") || env["TMPDIR"] != tmp {
		return nil, mainCap("agents private three-root native allocation")
	}
	for _, p := range []string{root, home, cwd, tmp} {
		real, e := filepath.EvalSymlinks(p)
		if e != nil || real != p {
			return nil, mainCap("agents canonical allocation")
		}
	}
	for k, v := range map[string]string{"USER_TYPE": "external", "NODE_ENV": "production", "TZ": "UTC", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1"} {
		if env[k] != v {
			return nil, mainCap("agents external native profile " + k)
		}
	}
	for _, key := range []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN", "ANTHROPIC_BASE_URL", "CLAUDE_CODE_OAUTH_TOKEN", "CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_REMOTE", "CLAUDE_CODE_USE_COWORK_PLUGINS", "DEBUG", "DEBUG_SDK", "NODE_OPTIONS", "CLAUDE_CODE_ACTION", "OTEL_EXPORTER_OTLP_ENDPOINT"} {
		if env[key] != "" {
			return nil, mainCap("agents nonempty external capability " + key)
		}
	}
	now := time.Now
	n := &NativeBoundary{Home: home, CWD: cwd, Temp: tmp, ManagedRoot: "/Library/Application Support/ClaudeCode", Environment: map[string]string{}, Now: now, NowMillis: func() float64 { return float64(now().UnixMilli()) }, ExternalAbsent: true, roots: []string{home, cwd, tmp}, absentGit: map[string]bool{}}
	for k, v := range env {
		n.Environment[k] = v
	}
	for d := filepath.Dir(cwd); ; d = filepath.Dir(d) {
		n.absentGit[filepath.Join(d, ".git")] = true
		if d == filepath.Dir(d) {
			break
		}
	}
	return n, nil
}

type mainAgentsFiles struct{ n *NativeBoundary }

func (f mainAgentsFiles) external(p string) bool { return inside(f.n.ManagedRoot, p) }
func (f mainAgentsFiles) Stat(p string) (fs.FileInfo, error) {
	if f.external(p) {
		f.n.observe("stat", p, "agents external frozen ENOENT")
		return nil, &os.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	i, e := f.n.stat(p)
	if e != nil && !errors.Is(e, fs.ErrNotExist) && !errors.Is(e, fs.ErrPermission) {
		return nil, &agentcatalog.CapabilityError{Boundary: e.Error()}
	}
	if e == nil {
		i = withAgentsNativeIdentity(p, i, false)
	}
	return i, e
}
func (f mainAgentsFiles) Lstat(p string) (fs.FileInfo, error) {
	i, e := f.n.lstat(p)
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return nil, &agentcatalog.CapabilityError{Boundary: e.Error()}
	}
	if e == nil {
		i = withAgentsNativeIdentity(p, i, true)
	}
	return i, e
}
func (f mainAgentsFiles) ReadFile(p string) ([]byte, error) {
	b, e := f.n.readFile(p)
	if e != nil && !errors.Is(e, fs.ErrNotExist) && !errors.Is(e, fs.ErrPermission) && !errors.Is(e, syscall.EISDIR) {
		return nil, &agentcatalog.CapabilityError{Boundary: e.Error()}
	}
	return b, e
}
func (f mainAgentsFiles) ReadDir(p string) ([]fs.DirEntry, error) {
	b, e := f.n.readDir(p)
	if e != nil && !errors.Is(e, fs.ErrNotExist) && !errors.Is(e, fs.ErrPermission) {
		return nil, &agentcatalog.CapabilityError{Boundary: e.Error()}
	}
	return b, e
}
func (f mainAgentsFiles) Realpath(p string) (string, error) {
	if e := f.n.check("realpath", p); e != nil {
		return "", &agentcatalog.CapabilityError{Boundary: e.Error()}
	}
	return filepath.EvalSymlinks(p)
}
func (f mainAgentsFiles) Identity(i fs.FileInfo) string {
	return agentsFileIdentity(i)
}
