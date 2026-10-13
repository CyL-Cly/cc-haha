package builtinwrite

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/analytics"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"unicode/utf16"
)

const unexpectedlyModified = "File has been unexpectedly modified. Read it again before attempting to write it."

func (s *Service) Validate(ctx context.Context, input protocol.JSONObject, scope *cooperative.Scope) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if e := cancelled(ctx); e != nil {
		return e
	}
	host, e := s.options.HostState(scope)
	if e != nil {
		return e
	}
	if host.TeamMemoryEnabled {
		return capability("team-memory path and secret scanning")
	}
	path, e := s.expand(input.Get("file_path"))
	if e != nil {
		return e
	}
	context, e := s.options.PermissionContext(scope)
	if e != nil {
		return e
	}
	_, deny, e := s.rules(scope, context, path, false)
	if e != nil {
		return e
	}
	if deny {
		return &ValidationError{"File is in a directory that is denied by your permission settings.", 1}
	}
	// Source returns valid without stat for UNC; permission resolution precedes
	// the physical call and is the only admission of network I/O.
	if unc(input.Get("file_path")) {
		return nil
	}
	f := s.options.Files
	future, e := native(s, scope, func() (float64, error) { return fileMtime(f, path) })
	if e != nil {
		return e
	}
	mtime, e := future.Await(scope)
	if errors.Is(e, fs.ErrNotExist) {
		return nil
	}
	if e != nil {
		return e
	}
	state, ok, e := s.options.Cache.Get(scope, path)
	if e != nil {
		return e
	}
	if !ok || state.PartialView {
		return &ValidationError{"File has not been read yet. Read it first before writing to it.", 2}
	}
	if mtime > state.Timestamp {
		return &ValidationError{"File has been modified since read, either by the user or by a linter. Read it again before attempting to write it.", 3}
	}
	return nil
}
func (s *Service) discoverSkills(ctx context.Context, scope *cooperative.Scope, path string) error {
	if s.options.FileSkills != nil {
		future, e := s.options.FileSkills(ctx, scope, path)
		if e != nil {
			return e
		}
		_, e = future.Await(scope)
		return e
	}
	f := s.options.Files
	cwd := strings.TrimSuffix(s.options.CWD, string(filepath.Separator))
	for dir := filepath.Dir(path); strings.HasPrefix(dir, cwd+string(filepath.Separator)); dir = filepath.Dir(dir) {
		for _, kind := range []string{".claude", ".agents"} {
			candidate := filepath.Join(dir, kind, "skills")
			if s.unportedSkillDirs[candidate] {
				return capability("discovered dynamic skill directory: " + candidate)
			}
			if s.dynamicSkillDirs[candidate] {
				continue
			}
			s.dynamicSkillDirs[candidate] = true
			future, e := native(s, scope, func() (bool, error) { _, e := f.Stat(candidate); return e == nil, e })
			if e != nil {
				return e
			}
			exists, _ := future.Await(scope)
			// Source catches the native stat's failure. A found directory requires the
			// real git-ignore/trust/skill loader, even if loading would later be disabled.
			if exists {
				s.unportedSkillDirs[candidate] = true
				return capability("discovered dynamic skill directory: " + candidate)
			}
		}
	}
	return nil
}

// ReadForCache is a native helper for the shared Read implementation/caller. It
// is not an injected Read result and does not register an unported Read tool.
func (s *Service) ReadForCache(scope *cooperative.Scope, inputPath json.RawMessage) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if s.options.Host.Platform != "windows" && unc(inputPath) {
		return capability("UNC file read")
	}
	path, e := s.expand(inputPath)
	if e != nil {
		return e
	}
	meta, e := readMetadata(s.options.Files, path)
	if e != nil {
		return e
	}
	mtime, e := fileMtime(s.options.Files, path)
	if e != nil {
		return e
	}
	return s.options.Cache.Set(scope, path, FileState{Content: contentRaw(meta.content), Timestamp: mtime})
}

// Call preserves the Source no-await section between native metadata read,
// staleness validation and the physical flushed replacement. Permission and
// original hooks are resolved by the existing SerialToolBatch before this call.
func (s *Service) Call(ctx context.Context, input protocol.JSONObject, scope *cooperative.Scope) (json.RawMessage, error) {
	return s.callWithParent(ctx, input, scope, nil)
}
func (s *Service) callWithParent(ctx context.Context, input protocol.JSONObject, scope *cooperative.Scope, parent json.RawMessage) (json.RawMessage, error) {
	if e := s.check(scope); e != nil {
		return nil, e
	}
	if e := cancelled(ctx); e != nil {
		return nil, e
	}
	if s.options.Host.Platform != "windows" && unc(input.Get("file_path")) {
		return nil, capability("UNC/NTLM network-path permission handling")
	}
	originalPathUnits, e := protocol.DecodeJavaScriptJSONString(input.Get("file_path"))
	if e != nil {
		return nil, e
	}
	path, e := s.expand(input.Get("file_path"))
	if e != nil {
		return nil, e
	}
	content, e := protocol.DecodeJavaScriptJSONString(input.Get("content"))
	if e != nil {
		return nil, e
	}
	if e = s.hostCheck(scope); e != nil {
		return nil, e
	}
	if e = s.discoverSkills(ctx, scope, path); e != nil {
		return nil, e
	}
	// discoverSkillDirsForPaths and disabled beforeFileEdited are real awaited
	// async returns in Source, including the zero-directory/disabled branches.
	if e = scope.AwaitFulfilled(); e != nil {
		return nil, e
	}
	if e = scope.AwaitFulfilled(); e != nil {
		return nil, e
	}
	f := s.options.Files
	dir := filepath.Dir(path)
	mkdir, e := native(s, scope, func() (struct{}, error) { return struct{}{}, f.Mkdir(dir) })
	if e != nil {
		return nil, e
	}
	if _, e = mkdir.Await(scope); e != nil {
		return nil, e
	}
	host, e := s.options.HostState(scope)
	if e != nil {
		return nil, e
	}
	if (!host.NonInteractive || truth(s.options.Environment("CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"))) && !truth(s.options.Environment("CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING")) {
		if s.options.FileHistoryTrack == nil {
			return nil, capability("enabled actual fileHistoryTrackEdit")
		}
		if e = s.options.FileHistoryTrack(scope, path, parent); e != nil {
			return nil, e
		}
	}
	meta, e := readMetadata(f, path)
	exists := e == nil
	if e != nil && !errors.Is(e, fs.ErrNotExist) {
		return nil, e
	}
	if exists {
		mtime, e := fileMtime(f, path)
		if e != nil {
			return nil, e
		}
		last, ok, e := s.options.Cache.Get(scope, path)
		if e != nil {
			return nil, e
		}
		if !ok || mtime > last.Timestamp {
			var previous []uint16
			if ok {
				previous, e = protocol.DecodeJavaScriptJSONString(last.Content)
				if e != nil {
					return nil, e
				}
			}
			fullRead := ok && last.Offset == nil && last.Limit == nil
			if !fullRead || !slices.Equal(meta.content, previous) {
				return nil, errors.New(unexpectedlyModified)
			}
		}
	}
	patch := []PatchHunk{}
	if exists && len(meta.content) > 0 {
		patch = StructuredPatch(ConvertLeadingTabsToSpaces(string(utf16.Decode(meta.content))), ConvertLeadingTabsToSpaces(string(utf16.Decode(content))))
	}
	encoding := "utf8"
	if exists {
		encoding = meta.encoding
	}
	if e = s.atomicWrite(path, contentBytes(content, encoding)); e != nil {
		return nil, e
	}
	mtime, e := fileMtime(f, path)
	if e != nil {
		return nil, e
	}
	if e = s.options.Cache.Set(scope, path, FileState{Content: contentRaw(content), Timestamp: mtime}); e != nil {
		return nil, e
	}
	// The mandatory total line change count is real Go state on this Owner.
	// Bootstrap LOC and enabled analytics are guarded until their callers migrate.
	if exists && len(meta.content) > 0 {
		for _, hunk := range patch {
			for _, line := range hunk.Lines {
				if strings.HasPrefix(line, "+") {
					s.addedLines++
				}
				if strings.HasPrefix(line, "-") {
					s.removedLines++
				}
			}
		}
	} else if len(content) > 0 {
		lines := 1
		for _, u := range content {
			if u == '\n' {
				lines++
			}
		}
		s.addedLines += lines
	}
	kind := "create"
	original := json.RawMessage("null")
	if exists && len(meta.content) > 0 {
		kind = "update"
		original = contentRaw(meta.content)
	}
	if s.options.ReportEvent != nil {
		for _, event := range analytics.WriteEvents(path, kind) {
			if e = s.options.ReportEvent(scope, event); e != nil {
				return nil, e
			}
		}
	}
	patchJSON, _ := json.Marshal(patch)
	data := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted(kind)}, protocol.JSONField{Name: "filePath", Value: contentRaw(originalPathUnits)}, protocol.JSONField{Name: "content", Value: contentRaw(content)}, protocol.JSONField{Name: "structuredPatch", Value: patchJSON}, protocol.JSONField{Name: "originalFile", Value: original})
	return raw(data), nil
}

// LineChanges is a value receipt read only on the original Owner. The existing
// cost/LOC consumers still need their own production bridge; this is not a log.
func (s *Service) LineChanges(scope *cooperative.Scope) (added, removed int, err error) {
	if err = s.check(scope); err != nil {
		return
	}
	return s.addedLines, s.removedLines, nil
}

// SetWorkingDirectory is the existing owner's mid-session Shell/worktree
// transition. It retains the Read/Write cache and original settings rule roots.
func (s *Service) SetWorkingDirectory(scope *cooperative.Scope, cwd string) error {
	if e := s.check(scope); e != nil {
		return e
	}
	if !filepath.IsAbs(cwd) {
		return capability("actual working directory")
	}
	s.options.CWD = cwd
	return nil
}
