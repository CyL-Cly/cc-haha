package clibootstrap

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type MainCapabilityError struct{ Boundary string }

func (e *MainCapabilityError) Error() string {
	return "Go public CLI capability is not migrated: " + e.Boundary
}
func mainCap(s string) error { return &MainCapabilityError{s} }

// NativeBoundary is an explicit external OS snapshot and primitive adapter.
// It cannot replace Controller, memory discovery, settings, permissions or SDK
// methods. The ordinary binary refuses unresolved policy/credential snapshots.
type NativeBoundary struct {
	Home, CWD, Temp, ManagedRoot string
	ImageLibraryPath             string
	// An explicit external path primitive for disposable CCR fixtures; ordinary
	// processes use the original /home/claude/.claude/remote location.
	CCRCredentialDirectory string
	Environment            map[string]string
	Now                    func() time.Time
	NowMillis              func() float64
	// Timer is the OS timer primitive; nil uses the production real clock.
	Timer             func(time.Duration, func()) func()
	ExternalAbsent    bool
	NativeProcess     bool
	ConfigDirExplicit bool
	ResolvePolicy     func() (*protocol.JSONObject, error)
	RunSecurity       func(context.Context, securestorage.Command) ([]byte, error)
	internalRead      *nativeInternalReadState
	mdm               *nativeMDMCache
	mu                sync.Mutex
	Trace             []NativeEvent
	roots             []string
	absentGit         map[string]bool
}
type NativeEvent struct{ Operation, Path, Outcome string }

func (n *NativeBoundary) observe(op, p, out string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	n.Trace = append(n.Trace, NativeEvent{op, p, out})
}
func inside(root, p string) bool {
	rel, e := filepath.Rel(root, p)
	return e == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) && !filepath.IsAbs(rel)
}
func (n *NativeBoundary) check(op, p string) error {
	if n.NativeProcess {
		n.observe(op, p, "native")
		return nil
	}
	if !filepath.IsAbs(p) || filepath.Clean(p) != p {
		n.observe(op, p, "deny:uncanonical")
		return mainCap("canonical native path")
	}
	accepted := ""
	for _, root := range n.roots {
		if inside(root, p) {
			accepted = root
			break
		}
	}
	if accepted == "" {
		n.observe(op, p, "deny:outside")
		return fmt.Errorf("offline native guard denied %s: %s", op, p)
	}
	for a := p; ; a = filepath.Dir(a) {
		i, e := os.Lstat(a)
		if errors.Is(e, fs.ErrNotExist) {
			continue
		}
		if e != nil {
			return e
		}
		if i.Mode()&fs.ModeSymlink != 0 {
			n.observe(op, p, "deny:symlink")
			return mainCap("native symlink provenance")
		}
		real, e := filepath.EvalSymlinks(a)
		if e != nil || !inside(accepted, real) {
			n.observe(op, p, "deny:ancestor")
			return mainCap("native nearest-existing-ancestor provenance")
		}
		break
	}
	n.observe(op, p, "admitted")
	return nil
}
func (n *NativeBoundary) stat(p string) (fs.FileInfo, error) {
	if n.absentGit[p] {
		n.observe("stat", p, "external frozen ENOENT")
		return nil, &os.PathError{Op: "stat", Path: p, Err: fs.ErrNotExist}
	}
	if e := n.check("stat", p); e != nil {
		return nil, e
	}
	return os.Stat(p)
}
func (n *NativeBoundary) lstat(p string) (fs.FileInfo, error) {
	if e := n.check("lstat", p); e != nil {
		return nil, e
	}
	return os.Lstat(p)
}
func (n *NativeBoundary) readFile(p string) ([]byte, error) {
	if e := n.check("readFile", p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (n *NativeBoundary) readDir(p string) ([]fs.DirEntry, error) {
	if e := n.check("readdir", p); e != nil {
		return nil, e
	}
	return os.ReadDir(p)
}
func (n *NativeBoundary) TransportOrigin() error {
	if n.NativeProcess {
		base := n.Environment["ANTHROPIC_BASE_URL"]
		if base == "" {
			base = "https://api.anthropic.com"
		}
		u, err := url.Parse(base)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
			return errors.New("invalid Anthropic Messages base URL")
		}
		return nil
	}
	u, e := url.Parse(n.Environment["ANTHROPIC_BASE_URL"])
	if e != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || u.User != nil {
		return mainCap("offline exact loopback transport origin")
	}
	if n.Environment["ANTHROPIC_API_KEY"] != "loopback-cli-tool-restart-no-quota-key" {
		return mainCap("offline fake credential")
	}
	return nil
}

// NewOfflineNativeBoundary admits only a freshly allocated three-root fixture.
// No actual machine policy, security/keychain, managed files or user HOME is read.
func NewOfflineNativeBoundary(env map[string]string, cwd string) (*NativeBoundary, error) {
	home := env["HOME"]
	root := filepath.Dir(home)
	tmp := filepath.Join(root, "tmp")
	if !filepath.IsAbs(root) || filepath.Base(home) != "home" || cwd != filepath.Join(root, "workspace") || env["USERPROFILE"] != home || env["CLAUDE_CONFIG_DIR"] != filepath.Join(home, ".claude") || env["TMPDIR"] != tmp {
		return nil, mainCap("offline native three-root layout")
	}
	for _, p := range []string{root, home, cwd, tmp} {
		real, e := filepath.EvalSymlinks(p)
		if e != nil || real != p {
			return nil, mainCap("offline fixture canonical allocation")
		}
	}
	if !strings.HasPrefix(filepath.Base(root), "cc-haha-") {
		return nil, mainCap("offline fixture allocation identity")
	}
	for k, v := range map[string]string{"NODE_ENV": "production", "USER_TYPE": "external", "TZ": "UTC", "BUN_OPTIONS": "--no-env-file", "USE_BUILTIN_RIPGREP": "false", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC": "1", "CLAUDE_CODE_MAX_RETRIES": "0", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX": "0", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK": "1", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"} {
		if env[k] != v {
			return nil, mainCap("offline native environment " + k)
		}
	}
	start := time.Now()
	now := func() time.Time { return time.UnixMilli(1700000000000 + time.Since(start).Milliseconds()).UTC() }
	managed := "/Library/Application Support/ClaudeCode"
	n := &NativeBoundary{Home: home, CWD: cwd, Temp: tmp, ManagedRoot: managed, Environment: map[string]string{}, Now: now, NowMillis: func() float64 { return float64(now().UnixMilli()) }, ExternalAbsent: true, roots: []string{home, cwd, tmp}, absentGit: map[string]bool{}}
	for k, v := range env {
		n.Environment[k] = v
	}
	for d := filepath.Dir(cwd); ; d = filepath.Dir(d) {
		n.absentGit[filepath.Join(d, ".git")] = true
		if filepath.Dir(d) == d {
			break
		}
	}
	for _, relative := range []string{"plugins", "agents", "skills", "commands", "workflows"} {
		p := filepath.Join(env["CLAUDE_CONFIG_DIR"], relative)
		es, e := n.readDir(p)
		if e == nil && len(es) > 0 {
			return nil, mainCap("active private startup " + relative)
		}
		if e != nil && !errors.Is(e, fs.ErrNotExist) {
			return nil, e
		}
	}
	if env["CC_HAHA_FIXTURE_TEMP_ROOT"] != "" && env["CC_HAHA_FIXTURE_TEMP_ROOT"] != root {
		return nil, mainCap("fixture allocation env identity")
	}
	if e := n.TransportOrigin(); e != nil {
		return nil, e
	}
	return n, nil
}

type nativeReadHandle struct {
	*os.File
	n       *NativeBoundary
	path    string
	initial fs.FileInfo
}

func (h *nativeReadHandle) ReadAt(b []byte, off int64) (int, error) {
	if e := h.n.check("readAt", h.path); e != nil {
		return 0, e
	}
	current, e := os.Stat(h.path)
	if e != nil || !os.SameFile(h.initial, current) {
		return 0, mainCap("borrowed descriptor provenance")
	}
	return h.File.ReadAt(b, off)
}
func (n *NativeBoundary) openRead(p string) (*nativeReadHandle, error) {
	if e := n.check("openRead", p); e != nil {
		return nil, e
	}
	f, e := os.Open(p)
	if e != nil {
		return nil, e
	}
	i, e := f.Stat()
	if e != nil {
		f.Close()
		return nil, e
	}
	return &nativeReadHandle{f, n, p, i}, nil
}

type mainSettingsFiles struct {
	clisettings.OSFileSystem
	n *NativeBoundary
}

func (f mainSettingsFiles) ReadFile(p string) ([]byte, error)   { return f.n.readFile(p) }
func (f mainSettingsFiles) Stat(p string) (fs.FileInfo, error)  { return f.n.stat(p) }
func (f mainSettingsFiles) Lstat(p string) (fs.FileInfo, error) { return f.n.lstat(p) }
func (f mainSettingsFiles) OpenReadFile(p string) (clisettings.ReadHandle, error) {
	return f.n.openRead(p)
}
func (f mainSettingsFiles) EvalSymlinks(p string) (string, error) {
	if e := f.n.check("realpath", p); e != nil {
		return "", e
	}
	return filepath.EvalSymlinks(p)
}
func (f mainSettingsFiles) Readlink(p string) (string, error) {
	if e := f.n.check("readlink", p); e != nil {
		return "", e
	}
	return os.Readlink(p)
}
func (f mainSettingsFiles) MkdirAll(p string) error {
	if e := f.n.check("mkdir", p); e != nil {
		return e
	}
	return f.OSFileSystem.MkdirAll(p)
}
func (f mainSettingsFiles) WriteFlush(p string, b []byte) error {
	if e := f.n.check("writeFlush", p); e != nil {
		return e
	}
	return f.OSFileSystem.WriteFlush(p, b)
}
func (f mainSettingsFiles) Chmod(p string, m fs.FileMode) error {
	if e := f.n.check("chmod", p); e != nil {
		return e
	}
	return os.Chmod(p, m)
}
func (f mainSettingsFiles) Rename(p, q string) error {
	if e := f.n.check("rename-from", p); e != nil {
		return e
	}
	if e := f.n.check("rename-to", q); e != nil {
		return e
	}
	return os.Rename(p, q)
}
func (f mainSettingsFiles) Remove(p string) error {
	if e := f.n.check("remove", p); e != nil {
		return e
	}
	return os.Remove(p)
}

type mainGlobalFiles struct {
	cliglobalconfig.OSFileSystem
	n *NativeBoundary
}

func (f mainGlobalFiles) ReadFile(p string) ([]byte, error) { return f.n.readFile(p) }
func (f mainGlobalFiles) Stat(p string) (cliglobalconfig.FileStat, error) {
	i, e := f.n.stat(p)
	if e != nil {
		return cliglobalconfig.FileStat{}, e
	}
	return cliglobalconfig.FileStat{Mtime: float64(i.ModTime().UnixNano()) / 1e6, Size: i.Size(), Mode: i.Mode()}, nil
}
func (f mainGlobalFiles) Exists(p string) bool { _, e := f.n.stat(p); return e == nil }
func (f mainGlobalFiles) MkdirAll(p string) error {
	if e := f.n.check("mkdirAll", p); e != nil {
		return e
	}
	return f.OSFileSystem.MkdirAll(p)
}
func (f mainGlobalFiles) Mkdir(p string) error {
	if e := f.n.check("mkdir", p); e != nil {
		return e
	}
	return f.OSFileSystem.Mkdir(p)
}
func (f mainGlobalFiles) Remove(p string) error {
	if e := f.n.check("remove", p); e != nil {
		return e
	}
	return os.Remove(p)
}
func (f mainGlobalFiles) ReadDir(p string) ([]string, error) {
	es, e := f.n.readDir(p)
	if e != nil {
		return nil, e
	}
	out := []string{}
	for _, x := range es {
		out = append(out, x.Name())
	}
	return out, nil
}
func (f mainGlobalFiles) CopyFile(p, q string) error {
	if e := f.n.check("copy-from", p); e != nil {
		return e
	}
	if e := f.n.check("copy-to", q); e != nil {
		return e
	}
	return f.OSFileSystem.CopyFile(p, q)
}
func (f mainGlobalFiles) Readlink(p string) (string, error) {
	return (mainSettingsFiles{n: f.n}).Readlink(p)
}
func (f mainGlobalFiles) Realpath(p string) (string, error) {
	return (mainSettingsFiles{n: f.n}).EvalSymlinks(p)
}
func (f mainGlobalFiles) Chmod(p string, m fs.FileMode) error {
	return (mainSettingsFiles{n: f.n}).Chmod(p, m)
}
func (f mainGlobalFiles) Chtimes(p string, t time.Time) error {
	if e := f.n.check("chtimes", p); e != nil {
		return e
	}
	return f.OSFileSystem.Chtimes(p, t)
}
func (f mainGlobalFiles) Rename(p, q string) error { return (mainSettingsFiles{n: f.n}).Rename(p, q) }
func (f mainGlobalFiles) WriteFlush(p string, b []byte, m *fs.FileMode) error {
	if e := f.n.check("writeFlush", p); e != nil {
		return e
	}
	return f.OSFileSystem.WriteFlush(p, b, m)
}

type mainResumeFiles struct {
	resumestate.OSFiles
	n *NativeBoundary
}
type mainStoreFile struct {
	sessionstore.File
	h *nativeReadHandle
}

func (f mainStoreFile) ReadAt(b []byte, o int64) (int, error) { return f.h.ReadAt(b, o) }
func (f mainStoreFile) Close() error {
	e := f.h.Close()
	e2 := f.File.Close()
	if e != nil {
		return e
	}
	return e2
}
func (f mainResumeFiles) Open(p string) (sessionstore.File, error) {
	// Keep the existing native sessionstore Snapshot producer. Both descriptors
	// are read-only and separately closed; reads retain provenance of the real fd.
	h, e := f.n.openRead(p)
	if e != nil {
		return nil, e
	}
	s, e := f.OSFiles.Open(p)
	if e != nil {
		h.Close()
		return nil, e
	}
	return mainStoreFile{s, h}, nil
}
func (f mainResumeFiles) ReadFile(p string) ([]byte, error)       { return f.n.readFile(p) }
func (f mainResumeFiles) ReadDir(p string) ([]fs.DirEntry, error) { return f.n.readDir(p) }
func (f mainResumeFiles) WriteFile(p string, b []byte) error {
	if e := f.n.check("writeFile", p); e != nil {
		return e
	}
	return f.OSFiles.WriteFile(p, b)
}
func (f mainResumeFiles) MkdirAll(p string) error {
	if e := f.n.check("mkdirAll", p); e != nil {
		return e
	}
	return f.OSFiles.MkdirAll(p)
}
func (f mainResumeFiles) Link(p, q string) error {
	if e := f.n.check("link-from", p); e != nil {
		return e
	}
	if e := f.n.check("link-to", q); e != nil {
		return e
	}
	return os.Link(p, q)
}
func (f mainResumeFiles) CopyFile(p, q string) error {
	if e := f.n.check("copy-from", p); e != nil {
		return e
	}
	if e := f.n.check("copy-to", q); e != nil {
		return e
	}
	return f.OSFiles.CopyFile(p, q)
}

type mainTranscriptFiles struct {
	sessioncontroller.OSFiles
	n         *NativeBoundary
	committed func(string) error
}

func (f mainTranscriptFiles) ReadFile(p string) ([]byte, error)  { return f.n.readFile(p) }
func (f mainTranscriptFiles) Stat(p string) (fs.FileInfo, error) { return f.n.stat(p) }
func (f mainTranscriptFiles) MkdirAll(p string, m fs.FileMode) error {
	if e := f.n.check("mkdirAll", p); e != nil {
		return e
	}
	return os.MkdirAll(p, m)
}
func (f mainTranscriptFiles) OpenFile(p string, flag int, m fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	if e := f.n.check("openWrite", p); e != nil {
		return nil, e
	}
	file, err := f.OSFiles.OpenFile(p, flag, m)
	if err != nil || f.committed == nil {
		return file, err
	}
	return &mainCommittedTranscriptFile{TranscriptFile: file, path: p, committed: f.committed}, nil
}

type mainCommittedTranscriptFile struct {
	sessioncontroller.TranscriptFile
	path                  string
	committed             func(string) error
	wrote, failed, closed bool
}

func (f *mainCommittedTranscriptFile) Write(data []byte) (int, error) {
	n, err := f.TranscriptFile.Write(data)
	f.wrote = f.wrote || n > 0
	f.failed = f.failed || err != nil || n != len(data)
	return n, err
}
func (f *mainCommittedTranscriptFile) Close() error {
	err := f.TranscriptFile.Close()
	if f.closed {
		return err
	}
	f.closed = true
	if err == nil && f.wrote && !f.failed {
		return f.committed(f.path)
	}
	return err
}

type mainWriteFiles struct{ mainSettingsFiles }

func (f mainWriteFiles) Realpath(p string) (string, error)                  { return f.EvalSymlinks(p) }
func (f mainWriteFiles) OpenRead(p string) (builtinwrite.ReadHandle, error) { return f.n.openRead(p) }
func (f mainWriteFiles) Mkdir(p string) error                               { return f.MkdirAll(p) }
func (f mainWriteFiles) Unlink(p string) error                              { return f.Remove(p) }

type mainDiagnosticFiles struct {
	diagnostics.OSFileSystem
	n *NativeBoundary
}

func (f mainDiagnosticFiles) MkdirAll(p string, m fs.FileMode) error {
	if e := f.n.check("diagnostic-mkdir", p); e != nil {
		return e
	}
	return f.OSFileSystem.MkdirAll(p, m)
}
func (f mainDiagnosticFiles) Lstat(p string) (fs.FileInfo, error) { return f.n.lstat(p) }
func (f mainDiagnosticFiles) Stat(p string) (fs.FileInfo, error)  { return f.n.stat(p) }
func (f mainDiagnosticFiles) Exists(p string) bool                { _, e := f.n.stat(p); return e == nil }
func (f mainDiagnosticFiles) Chmod(p string, m fs.FileMode) error {
	return (mainSettingsFiles{n: f.n}).Chmod(p, m)
}
func (f mainDiagnosticFiles) Append(p string, b []byte, m fs.FileMode) error {
	if e := f.n.check("diagnostic-append", p); e != nil {
		return e
	}
	return f.OSFileSystem.Append(p, b, m)
}
func (f mainDiagnosticFiles) Rename(p, q string) error {
	return (mainSettingsFiles{n: f.n}).Rename(p, q)
}
func (f mainDiagnosticFiles) ReadDir(p string) ([]string, error) {
	return (mainGlobalFiles{n: f.n}).ReadDir(p)
}
func (f mainDiagnosticFiles) Remove(p string) error { return (mainSettingsFiles{n: f.n}).Remove(p) }
