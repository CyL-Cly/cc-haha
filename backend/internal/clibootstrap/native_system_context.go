package clibootstrap

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func trimSystemContext(v string) string {
	return strings.TrimFunc(v, func(r rune) bool { return r == '\ufeff' || r != '\u0085' && unicode.IsSpace(r) })
}

func nativeGitInstructions(env map[string]string, settings *protocol.JSONObject) bool {
	value := env["CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS"]
	if envTruthyNative(value) {
		return false
	}
	switch strings.ToLower(trimSystemContext(value)) {
	case "0", "false", "no", "off":
		return true
	}
	return settings == nil || string(settings.Get("includeGitInstructions")) != "false"
}

// This is the same Source snapshot rendered into both ASK and inspection. Its
// UTF-16 cutoff deliberately preserves a surrogate split at character 2,000.
func nativeGitStatus(branch, main, status, log, user string) json.RawMessage {
	units := utf16.Encode([]rune(status))
	if len(units) > 2000 {
		units = append(units[:2000:2000], utf16.Encode([]rune("\n... (truncated because it exceeds 2k characters. If you need more information, run \"git status\" using BashTool)"))...)
	}
	if len(units) == 0 {
		units = utf16.Encode([]rune("(clean)"))
	}
	parts := []string{
		"This is the git status at the start of the conversation. Note that this status is a snapshot in time, and will not update during the conversation.",
		"Current branch: " + branch,
		"Main branch (you will usually use this for PRs): " + main,
	}
	if user != "" {
		parts = append(parts, "Git user: "+user)
	}
	result := utf16.Encode([]rune(strings.Join(parts, "\n\n") + "\n\nStatus:\n"))
	result = append(result, units...)
	result = append(result, utf16.Encode([]rune("\n\nRecent commits:\n"+log))...)
	return protocol.QuoteJavaScriptUTF16(result)
}

func (m *MainPrint) runContextGit(lifetime context.Context, cwd, executable string, args []string) string {
	ctx, cancel := context.WithTimeout(lifetime, 10*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = cwd
	command.Env = nativeAgentProcessEnvironment(m.options.Native)
	data, err := command.Output()
	if err != nil {
		return ""
	} // execFileNoThrow preserveOutputOnError=false
	return trimSystemContext(string(protocol.DecodeJavaScriptUTF8(data)))
}

func (m *MainPrint) nativeSystemContext(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if m.options.Arguments.SystemPrompt != "" || m.options.Arguments.HasCustomSystemPrompt {
		return protocol.NewJSONObject(), nil
	}
	return m.nativeInspectionSystemContext(scope)
}
func (m *MainPrint) nativeInspectionSystemContext(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if !scope.BelongsTo(m.Owner) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	if !m.options.Native.NativeProcess {
		return protocol.NewJSONObject(), nil
	}
	future, err := m.beginNativeInspectionSystemContext(scope)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	return future.Await(scope)
}

func (m *MainPrint) beginNativeSystemContext(scope *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	if _, _, err := m.nativePromptProducer(scope); err != nil {
		return nil, err
	}
	if m.options.Arguments.SystemPrompt != "" || m.options.Arguments.HasCustomSystemPrompt {
		future, err := ownerasync.NewOwnerFuture[protocol.JSONObject](m.Owner)
		if err != nil {
			return nil, err
		}
		return future, future.Resolve(scope, protocol.NewJSONObject())
	}
	return m.beginNativeInspectionSystemContext(scope)
}

func (m *MainPrint) beginNativeInspectionSystemContext(scope *cooperative.Scope) (*ownerasync.OwnerFuture[protocol.JSONObject], error) {
	life, err := m.nativeInspectionContextLifetime(scope)
	if err != nil {
		return nil, err
	}
	if m.systemContext != nil {
		return m.systemContext, nil
	}
	settings, err := m.nativeSDKInitialSettings()
	if err != nil {
		return nil, err
	}
	result, err := ownerasync.NewOwnerFuture[protocol.JSONObject](m.Owner)
	if err != nil {
		return nil, err
	}
	m.systemContext = result
	// getSystemContext's original prefix starts its Git futures and registers
	// their reactions without awaiting them here. A legitimate inspection Read
	// can start that same memo; no independent prompt stack or model authority
	// is needed for this callback, and the caller awaits the returned future.
	err = m.populateNativeSystemContext(scope, life, settings, result)
	if err != nil {
		_ = result.RejectAdministrative(err)
	}
	return result, err
}

// This existing native MainPrint factory minted the prompt lifetime during
// initializeOnOwner. Inspection only borrows that exact graph; it cannot mint a
// continuation from an arbitrary Read callback or relax the prompt Start guard.
func (m *MainPrint) nativeInspectionContextLifetime(scope *cooperative.Scope) (context.Context, error) {
	if m == nil || m.options.Native == nil || !m.options.Native.NativeProcess || !scope.BelongsTo(m.Owner) || m.userContext == nil || m.settings == nil || !m.userContext.SharesSettings(m.settings, m.settingState, m.environment) || !m.settings.UsesNativeUserSettingsPath(m.settingState) {
		return nil, cooperative.ErrRestricted
	}
	if err := scope.Inline(cooperative.Read, func(*cooperative.Scope) error { return nil }); err != nil {
		return nil, err
	}
	r := &m.nativePromptLife
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil, cooperative.ErrClosed
	}
	if r.lanes == nil || !r.lanes.Owns(m.Owner) || r.life == nil || r.cancel == nil || r.user != m.userContext || r.settings != m.settings || r.state != m.settingState || r.environment != m.environment || r.startup != m.Startup {
		return nil, cooperative.ErrRestricted
	}
	return r.life, nil
}

func (m *MainPrint) populateNativeSystemContext(scope *cooperative.Scope, life context.Context, settings *protocol.JSONObject, result *ownerasync.OwnerFuture[protocol.JSONObject]) error {
	n := m.options.Native
	if envTruthyNative(n.Environment["CLAUDE_CODE_REMOTE"]) || !nativeGitInstructions(n.Environment, settings) || n.Environment["NODE_ENV"] == "test" {
		err := result.Resolve(scope, protocol.NewJSONObject())
		if err != nil {
			return err
		}
		return nil
	}
	cwd := m.workingDirectory()
	root, err := m.Git.FindRoot(scope, cwd)
	if err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	if root == "" {
		if err := result.Resolve(scope, protocol.NewJSONObject()); err != nil {
			return err
		}
		return nil
	}
	branch, err := m.Git.Branch(scope)
	if err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	main, err := m.Git.DefaultBranch(scope)
	if err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	executable := "git"
	for _, dir := range filepath.SplitList(n.Environment["PATH"]) {
		if dir == "" {
			dir = cwd
		}
		if !filepath.IsAbs(dir) {
			dir = filepath.Join(cwd, dir)
		}
		name := "git"
		if m.options.Platform == "windows" {
			name += ".exe"
		}
		candidate := filepath.Join(dir, name)
		if info, e := os.Stat(candidate); e == nil && !info.IsDir() && (m.options.Platform == "windows" || info.Mode()&0111 != 0) {
			executable = candidate
			break
		}
	}
	commands, err := nativeSkillWork(m, scope, func() ([3]string, error) {
		var rows [3]string
		var jobs sync.WaitGroup
		for i, args := range [][]string{{"--no-optional-locks", "status", "--short"}, {"--no-optional-locks", "log", "--oneline", "-n", "5"}, {"config", "user.name"}} {
			jobs.Add(1)
			go func(i int, args []string) { defer jobs.Done(); rows[i] = m.runContextGit(life, cwd, executable, args) }(i, args)
		}
		jobs.Wait()
		return rows, nil
	})
	if err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	// Resolve the shared memo from its producer reactions, independently of any
	// awaiting consumer stack. An inspector may enter while ASK awaits the same
	// memo; nesting a second await must not suspend the only stack that can finish
	// the original Git snapshot.
	var branchValue, mainValue string
	var rows [3]string
	remaining, settled := 3, false
	finish := func(next *cooperative.Scope, failure error) {
		if settled {
			return
		}
		remaining--
		if failure == nil && remaining != 0 {
			return
		}
		settled = true
		if failure := context.Cause(life); failure != nil {
			_ = result.Reject(next, failure)
			return
		}
		value := protocol.NewJSONObject()
		if failure == nil {
			value.Set("gitStatus", nativeGitStatus(branchValue, mainValue, rows[0], rows[1], rows[2]))
		}
		// Source Promise.all catches the first Git failure and caches null. Other
		// admitted physical jobs retain their existing delivery/cleanup ownership.
		if err := result.Resolve(next, value); err != nil {
			_ = result.RejectAdministrative(err)
		}
	}
	if err := branch.ThenOwner(scope, func(next *cooperative.Scope, value string, failure error) {
		branchValue = value
		finish(next, failure)
	}, func(failure error) { _ = result.RejectAdministrative(failure) }); err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	if err := main.ThenOwner(scope, func(next *cooperative.Scope, value string, failure error) {
		mainValue = value
		finish(next, failure)
	}, func(failure error) { _ = result.RejectAdministrative(failure) }); err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	if _, err := commands.Then(scope, func(next *cooperative.Scope, value [3]string, failure error) error {
		rows = value
		finish(next, failure)
		return nil
	}); err != nil {
		_ = result.Reject(scope, err)
		return err
	}
	return nil
}
