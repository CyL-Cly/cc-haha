package clibootstrap

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/worktree"
	"golang.org/x/text/unicode/norm"
)

type TerminalIDEDiffResult struct {
	Allowed bool
	Input   protocol.JSONObject
}
type nativeTerminalIDEDiffRuntime struct {
	mu      sync.Mutex
	closing bool
	jobs    map[*TerminalIDEDiff]bool
}
type TerminalIDEDiff struct {
	Name                                             string
	owner                                            *cooperative.Owner
	runtime                                          *nativeTerminalIDEDiffRuntime
	cancel, closeCancel                              context.CancelFunc
	dead                                             atomic.Bool
	future                                           *ownerasync.OwnerFuture[TerminalIDEDiffResult]
	closeFuture                                      *ownerasync.OwnerFuture[struct{}]
	cleanupReady, cleanupStart, closeSent, closeDone chan struct{}
	readyOnce, cleanupOnce, sentOnce                 sync.Once
	skipCleanup                                      atomic.Bool
	resultPublished                                  atomic.Bool
}

func (job *TerminalIDEDiff) readyForCleanup(skip bool) {
	if skip {
		job.skipCleanup.Store(true)
	}
	job.readyOnce.Do(func() { close(job.cleanupReady) })
}
func (job *TerminalIDEDiff) scheduleCleanup() {
	job.cleanupOnce.Do(func() { go func() { <-job.cleanupReady; close(job.cleanupStart) }() })
}

func (job *TerminalIDEDiff) cancelCleanupAfterSend() {
	go func() {
		select {
		case <-job.closeSent:
		case <-job.closeDone:
		}
		job.closeCancel()
	}()
}

func (job *TerminalIDEDiff) retireIfComplete() {
	if !job.resultPublished.Load() {
		return
	}
	select {
	case <-job.closeDone:
		job.runtime.mu.Lock()
		delete(job.runtime.jobs, job)
		job.runtime.mu.Unlock()
	default:
	}
}

func (m *MainPrint) BeginNativeTerminalIDEDiffClose() {
	runtime := &m.nativeTerminalIDEDiffs
	runtime.mu.Lock()
	runtime.closing = true
	jobs := []*TerminalIDEDiff{}
	for job := range runtime.jobs {
		jobs = append(jobs, job)
	}
	runtime.mu.Unlock()
	for _, job := range jobs {
		job.dead.Store(true)
		job.scheduleCleanup()
		job.cancel()
		job.cancelCleanupAfterSend()
	}
}

func (job *TerminalIDEDiff) StopOnOwner(scope *cooperative.Scope) {
	if job == nil || !scope.BelongsTo(job.owner) {
		return
	}
	job.dead.Store(true)
	job.scheduleCleanup()
	job.cancel()
	job.cancelCleanupAfterSend()
	_, _ = job.future.Await(scope)
	_, _ = job.closeFuture.Await(scope)
	select {
	case <-job.closeDone:
		job.closeCancel()
		job.retireIfComplete()
	default:
	}
}

func terminalIDEReadContents(native *NativeBoundary, path string) (string, error) {
	data, err := native.readFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	units := utf16.Encode([]rune(string(protocol.DecodeJavaScriptUTF8(data))))
	if len(data) >= 2 && data[0] == 0xff && data[1] == 0xfe {
		units = make([]uint16, len(data)/2)
		for i := range units {
			units[i] = binary.LittleEndian.Uint16(data[i*2:])
		}
	}
	return strings.ReplaceAll(terminalIDEUnitText(units), "\r\n", "\n"), nil
}

// The line diff compares byte strings. Map every non-ASCII UTF-16 unit to one
// private scalar while leaving the ASCII line separators and diff tokens intact.
// This is an internal reversible projection; wire/tool values use the original
// code units, so a lone surrogate never becomes a replacement character.
func terminalIDEUnitText(units []uint16) string {
	var text strings.Builder
	for _, unit := range units {
		if unit < 128 {
			text.WriteByte(byte(unit))
		} else {
			text.WriteRune(rune(unit) + 0x10000)
		}
	}
	return text.String()
}
func terminalIDETextJSON(value json.RawMessage) string {
	units, _ := protocol.DecodeJavaScriptJSONString(value)
	return terminalIDEUnitText(units)
}
func terminalIDEQuoteText(value string) json.RawMessage {
	units := []uint16{}
	for _, scalar := range value {
		if scalar < 128 {
			units = append(units, uint16(scalar))
		} else {
			units = append(units, uint16(scalar-0x10000))
		}
	}
	return protocol.QuoteJavaScriptUTF16(units)
}

func terminalIDEProposal(tool string, input protocol.JSONObject, old string) (string, error) {
	if tool == "Write" {
		return terminalIDETextJSON(input.Get("content")), nil
	}
	search, replacement := terminalIDETextJSON(input.Get("old_string")), terminalIDETextJSON(input.Get("new_string"))
	if search == "" {
		if old == "" && replacement == "" {
			return "", nil
		}
		if old == replacement {
			return "", errors.New("String not found in file. Failed to apply edit.")
		}
		return replacement, nil
	}
	if replacement == "" && !strings.HasSuffix(search, "\n") && strings.Contains(old, search+"\n") {
		search += "\n"
	}
	count := 1
	if string(input.Get("replace_all")) == "true" {
		count = -1
	}
	updated := strings.Replace(old, search, replacement, count)
	if updated == old {
		return "", errors.New("String not found in file. Failed to apply edit.")
	}
	return updated, nil
}

func terminalIDEUpdatedInput(tool string, input protocol.JSONObject, old, updated string) (protocol.JSONObject, bool) {
	escape := func(value string) string {
		return strings.ReplaceAll(strings.ReplaceAll(value, "&", "<<:AMPERSAND_TOKEN:>>"), "$", "<<:DOLLAR_TOKEN:>>")
	}
	unescape := func(value string) string {
		return strings.ReplaceAll(strings.ReplaceAll(value, "<<:AMPERSAND_TOKEN:>>", "&"), "<<:DOLLAR_TOKEN:>>", "$")
	}
	patch := builtinwrite.StructuredPatchSingle(escape(old), escape(updated))
	if len(patch) == 0 {
		next := input.Clone()
		if tool == "Edit" {
			next.Set("replace_all", rawMain(string(input.Get("replace_all")) == "true"))
		}
		return next, false
	}
	oldLines, newLines := []string{}, []string{}
	for _, line := range patch[0].Lines {
		line = unescape(line)
		if len(line) == 0 {
			continue
		}
		switch line[0] {
		case ' ':
			oldLines = append(oldLines, line[1:])
			newLines = append(newLines, line[1:])
		case '-':
			oldLines = append(oldLines, line[1:])
		case '+':
			newLines = append(newLines, line[1:])
		}
	}
	next := input.Clone()
	if tool == "Write" {
		next.Set("content", terminalIDEQuoteText(strings.Join(newLines, "\n")))
	} else {
		next.Set("old_string", terminalIDEQuoteText(strings.Join(oldLines, "\n")))
		next.Set("new_string", terminalIDEQuoteText(strings.Join(newLines, "\n")))
		next.Set("replace_all", []byte("false"))
	}
	return next, true
}

func terminalIDEDiffResponse(result protocol.JSONObject) (string, string, error) {
	var content []protocol.JSONObject
	if err := json.Unmarshal(result.Get("content"), &content); err != nil || len(content) == 0 || content[0].String("type") != "text" {
		return "", "", errors.New("Not accepted")
	}
	status := content[0].String("text")
	switch status {
	case "FILE_SAVED":
		if len(content) < 2 || len(content[1].Get("text")) == 0 || content[1].Get("text")[0] != '"' {
			return "", "", errors.New("Not accepted")
		}
		return status, terminalIDETextJSON(content[1].Get("text")), nil
	case "TAB_CLOSED", "DIFF_REJECTED":
		return status, "", nil
	}
	return "", "", errors.New("Not accepted")
}

func (m *MainPrint) BeginTerminalIDEDiff(ctx context.Context, scope *cooperative.Scope, tool string, input protocol.JSONObject, onResult func(*cooperative.Scope, TerminalIDEDiffResult, error) error) (*TerminalIDEDiff, error) {
	if !scope.BelongsTo(m.Owner) {
		return nil, cooperative.ErrRestricted
	}
	if !m.options.Interactive || m.agentChild || m.teamWorker != nil || m.mcpTools == nil || (tool != "Edit" && tool != "Write") || strings.HasSuffix(input.String("file_path"), ".ipynb") {
		return nil, nil
	}
	global, err := m.Global.Get()
	if err != nil {
		return nil, err
	}
	if global.String("diffTool") != "auto" {
		return nil, nil
	}
	client, config, err := m.mcpTools.ConnectedIDE(scope)
	if err != nil || client == nil {
		return nil, err
	}
	path := protocol.TrimJavaScriptString(input.String("file_path"))
	if path == "~" {
		path = m.options.Native.Home
	} else if strings.HasPrefix(path, "~/") {
		path = filepath.Join(m.options.Native.Home, path[2:])
	} else if !filepath.IsAbs(path) {
		path = filepath.Join(m.workingDirectory(), path)
	}
	path = norm.NFC.String(filepath.Clean(path))
	isWSL := m.options.Platform == "wsl" || m.options.Platform == "linux" && m.options.Native.Environment["WSL_DISTRO_NAME"] != ""
	windowsIDE := isWSL && string(config.Get("ideRunningInWindows")) == "true" && m.options.Native.Environment["WSL_DISTRO_NAME"] != ""
	suffix := make([]byte, 3)
	if _, err = rand.Read(suffix); err != nil {
		return nil, err
	}
	tab := "✻ [Claude Code] " + filepath.Base(path) + " (" + hex.EncodeToString(suffix) + ") ⧉"
	workCtx, cancel := context.WithCancel(ctx)
	closeCtx, closeCancel := context.WithCancel(context.Background())
	job := &TerminalIDEDiff{Name: config.String("ideName"), owner: m.Owner, runtime: &m.nativeTerminalIDEDiffs, cancel: cancel, closeCancel: closeCancel, cleanupReady: make(chan struct{}), cleanupStart: make(chan struct{}), closeSent: make(chan struct{}), closeDone: make(chan struct{})}
	if job.Name == "" {
		job.Name = "IDE"
	}
	runtime := job.runtime
	runtime.mu.Lock()
	if runtime.closing {
		runtime.mu.Unlock()
		cancel()
		closeCancel()
		return nil, cooperative.ErrClosed
	}
	if runtime.jobs == nil {
		runtime.jobs = map[*TerminalIDEDiff]bool{}
	}
	detached, native := input.Clone(), m.options.Native.agentScopedBoundary(m.workingDirectory())
	job.closeFuture, err = nativeSkillWork(m, scope, func() (struct{}, error) {
		defer func() {
			close(job.closeDone)
			job.retireIfComplete()
		}()
		<-job.cleanupStart
		if job.skipCleanup.Load() {
			return struct{}{}, nil
		}
		settled := make(chan struct{})
		_, failure := client.CallToolSourceWithSent(closeCtx, quoteMain("close_tab"), protocol.NewJSONObject(protocol.JSONField{Name: "tab_name", Value: quoteMain(tab)}), protocol.JSONObject{}, func() { close(settled) }, func(error) { job.sentOnce.Do(func() { close(job.closeSent) }) })
		<-settled
		return struct{}{}, failure
	})
	if err != nil {
		runtime.mu.Unlock()
		cancel()
		closeCancel()
		return nil, err
	}
	job.future, err = nativeSkillWork(m, scope, func() (TerminalIDEDiffResult, error) {
		// Source publishes positive/error results without awaiting cleanup.
		// Keep that RPC in a separate owned worker until unmount/exit joins it.
		old, err := terminalIDEReadContents(native, path)
		if err != nil {
			job.readyForCleanup(true)
			job.scheduleCleanup()
			return TerminalIDEDiffResult{}, err
		}
		job.readyForCleanup(false)
		defer job.scheduleCleanup()
		proposal, err := terminalIDEProposal(tool, detached, old)
		if err != nil {
			return TerminalIDEDiffResult{}, err
		}
		idePath := path
		if windowsIDE {
			idePath = terminalIDEWindowsPath(workCtx, native, path)
		}
		params := protocol.NewJSONObject(protocol.JSONField{Name: "old_file_path", Value: quoteMain(idePath)}, protocol.JSONField{Name: "new_file_path", Value: quoteMain(idePath)}, protocol.JSONField{Name: "new_file_contents", Value: terminalIDEQuoteText(proposal)}, protocol.JSONField{Name: "tab_name", Value: quoteMain(tab)})
		settled := make(chan struct{})
		result, err := client.CallToolSource(workCtx, quoteMain("openDiff"), params, protocol.JSONObject{}, func() { close(settled) })
		<-settled
		if err != nil {
			return TerminalIDEDiffResult{}, err
		}
		status, newContent, err := terminalIDEDiffResponse(result)
		if err != nil {
			return TerminalIDEDiffResult{}, err
		}
		if status == "TAB_CLOSED" {
			newContent = proposal
		}
		if status == "DIFF_REJECTED" {
			newContent = old
		}
		next, allowed := terminalIDEUpdatedInput(tool, detached, old, newContent)
		job.scheduleCleanup()
		if !allowed {
			secondSettled := make(chan struct{})
			_, _ = client.CallToolSource(closeCtx, quoteMain("close_tab"), protocol.NewJSONObject(protocol.JSONField{Name: "tab_name", Value: quoteMain(tab)}), protocol.JSONObject{}, func() { close(secondSettled) })
			<-secondSettled
		}
		return TerminalIDEDiffResult{Allowed: allowed, Input: next}, nil
	})
	if err != nil {
		cancel()
		closeCancel()
		runtime.mu.Unlock()
		job.readyForCleanup(true)
		job.scheduleCleanup()
		_, _ = job.closeFuture.Await(scope)
		return nil, err
	}
	runtime.jobs[job] = true
	runtime.mu.Unlock()
	_, err = job.future.Then(scope, func(next *cooperative.Scope, value TerminalIDEDiffResult, failure error) error {
		defer func() {
			job.resultPublished.Store(true)
			job.retireIfComplete()
		}()
		if job.dead.Load() {
			return nil
		}
		return onResult(next, value, failure)
	})
	if err != nil {
		job.StopOnOwner(scope)
		return nil, err
	}
	return job, nil
}

func terminalIDEWindowsPath(ctx context.Context, native *NativeBoundary, path string) string {
	if path == "" {
		return path
	}
	for _, directory := range filepath.SplitList(native.Environment["PATH"]) {
		candidate := filepath.Join(directory, "wslpath")
		info, err := os.Stat(candidate)
		if err != nil || info.IsDir() || info.Mode()&0111 == 0 {
			continue
		}
		result, err := worktree.NativeRun(ctx, worktree.ProcessRequest{Command: candidate, Arguments: []string{"-w", path}, CWD: native.CWD, Environment: native.Environment, IgnoreStdin: true})
		if err == nil && result.Code == 0 {
			return protocol.TrimJavaScriptString(result.Stdout)
		}
		break
	}
	return path
}
