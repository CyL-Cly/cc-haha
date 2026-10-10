package clibootstrap

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/modelaccounting"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"golang.org/x/text/unicode/norm"
	"path/filepath"
	"strings"
)

func (m *MainPrint) restoreProjectCosts(scope *cooperative.Scope, sid string) (bool, error) {
	// getCurrentProjectConfig uses originalCWD, not the adopted transcript dir.
	key, e := m.originalProjectConfigKey(scope)
	if e != nil {
		return false, e
	}
	project, e := m.Global.GetProject(key)
	if e != nil {
		return false, e
	}
	ledger, e := m.Controller.ModelAccountingFor(m.Owner)
	if e != nil {
		return false, e
	}
	resolver := modelaccounting.RestoredModelResolver{Environment: m.options.Native.Environment, Settings: m.Startup.GetInitialSettings, Global: m.Global.Get}
	return ledger.RestoreCostStateForSession(scope, sid, project, resolver, m.options.Native.NowMillis)
}

func trimProjectFile(data []byte) string {
	return strings.TrimFunc(string(protocol.DecodeJavaScriptUTF8(data)), func(r rune) bool {
		return r == '\ufeff' || r == '\u0009' || r == '\u000b' || r == '\u000c' || r == '\u0020' || r == '\u00a0' || r == '\u1680' || r >= '\u2000' && r <= '\u200a' || r == '\u202f' || r == '\u205f' || r == '\u3000' || r == '\n' || r == '\r' || r == '\u2028' || r == '\u2029'
	})
}
func resolveProjectPath(base, value string) string {
	if filepath.IsAbs(value) {
		return filepath.Clean(value)
	}
	return filepath.Join(base, value)
}
func (m *MainPrint) canonicalProjectRoot(root string) string {
	n := m.options.Native
	content, e := n.readFile(filepath.Join(root, ".git"))
	if e != nil {
		return root
	}
	text := trimProjectFile(content)
	if !strings.HasPrefix(text, "gitdir:") {
		return root
	}
	gitdir := resolveProjectPath(root, trimProjectFile([]byte(text[len("gitdir:"):])))
	data, e := n.readFile(filepath.Join(gitdir, "commondir"))
	if e != nil {
		return root
	}
	common := resolveProjectPath(gitdir, trimProjectFile(data))
	if filepath.Dir(gitdir) != filepath.Join(common, "worktrees") {
		return root
	}
	data, e = n.readFile(filepath.Join(gitdir, "gitdir"))
	if e != nil {
		return root
	}
	fs := mainGlobalFiles{n: n}
	backlink, e := fs.Realpath(trimProjectFile(data))
	if e != nil {
		return root
	}
	realroot, e := fs.Realpath(root)
	if e != nil {
		return root
	}
	if backlink != filepath.Join(realroot, ".git") {
		return root
	}
	if filepath.Base(common) != ".git" {
		return norm.NFC.String(common)
	}
	return norm.NFC.String(filepath.Dir(common))
}
func (m *MainPrint) originalProjectConfigKey(scope *cooperative.Scope) (string, error) {
	original := m.state.Snapshot().OriginalCWD
	resolved, e := filepath.Abs(original)
	if e != nil {
		return "", e
	}
	cacheKey := strings.ReplaceAll(filepath.Clean(resolved), "\\", "/")
	if key, ok := m.projectConfigKeys[cacheKey]; ok {
		return key, nil
	}
	root, e := m.Git.FindRoot(scope, original)
	if e != nil {
		return "", e
	}
	key := resolved
	if root != "" {
		key = m.canonicalProjectRoot(root)
	}
	key = strings.ReplaceAll(filepath.Clean(key), "\\", "/")
	if m.projectConfigKeys == nil {
		m.projectConfigKeys = map[string]string{}
	}
	m.projectConfigKeys[cacheKey] = key
	return key, nil
}
