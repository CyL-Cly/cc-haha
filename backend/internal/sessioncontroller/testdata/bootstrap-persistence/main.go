// This original-control fixture uses one real owner, writer, cache, and Git
// service. Source results never enter its runtime input. Native observations
// and administrative close are separate from the original program.
package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"runtime/debug"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/gitbranch"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type object map[string]json.RawMessage
type profile struct {
	ID         string                `json:"id"`
	SessionIDs map[string]string     `json:"sessionIds"`
	Messages   []protocol.JSONObject `json:"messages"`
	Operations []object              `json:"operations"`
	NativeGit  *struct {
		Kind, HeadBytes, ControlToken, LateSID, LateSlug string
		LateOriginalCWD                                  json.RawMessage `json:"lateOriginalCwd"`
		LateCWD                                          json.RawMessage `json:"lateCwd"`
	} `json:"nativeGit"`
}
type program struct {
	home             string
	input            profile
	state            *bootstrapstate.State
	cwd              *bootstrapstate.CWDContext
	cache            *bootstrapstate.PlanSlugs
	writer           *sessioncontroller.Writer
	pending          *sessioncontroller.PendingWriter
	loader           *sessionhistory.Loader
	git              *gitbranch.Service
	watch            *gitbranch.NativePollWatch
	trace            []any
	snapshots        []any
	errors           []any
	lastRecord       *sessioncontroller.RecordFuture
	absentPaths      []string
	cleanupCallbacks []func(*cooperative.Scope) error
}

func quote(value string) json.RawMessage { b, _ := json.Marshal(value); return b }
func str(op object, key string) string   { var s string; _ = json.Unmarshal(op[key], &s); return s }
func objectValue(raw json.RawMessage) (object, error) {
	var o object
	if e := json.Unmarshal(raw, &o); e != nil || o == nil {
		return nil, errors.New("original object required")
	}
	return o, nil
}
func allowed(o object, fields ...string) bool {
	for k := range o {
		found := false
		for _, f := range fields {
			if f == k {
				found = true
			}
		}
		if !found {
			return false
		}
	}
	return true
}
func pathValue(home string, raw json.RawMessage) (string, error) {
	o, e := objectValue(raw)
	if e != nil || len(o) != 1 || !allowed(o, "private") {
		return "", errors.New("original private leaf required")
	}
	leaf := str(o, "private")
	if leaf == "" || leaf == "." || leaf == ".." || strings.ContainsAny(leaf, "/\\\x00") {
		return "", errors.New("original private path required")
	}
	return filepath.Join(home, leaf), nil
}

var uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

func validate(home string, raw []byte) (profile, error) {
	var input profile
	o, e := objectValue(raw)
	if e != nil || !allowed(o, "id", "sessionIds", "messages", "operations", "nativeGit") {
		return input, errors.New("original finite persistence fields required")
	}
	if e = json.Unmarshal(raw, &input); e != nil {
		return input, e
	}
	if input.ID == "" || len(input.SessionIDs) != 2 || !uuidPattern.MatchString(input.SessionIDs["A"]) || !uuidPattern.MatchString(input.SessionIDs["B"]) || len(input.Messages) != 4 || input.Operations == nil || len(input.Operations) > 32 {
		return input, errors.New("original finite persistence shape required")
	}
	for _, m := range input.Messages {
		var payload protocol.JSONObject
		var timestamp string
		if !m.IsObject() || m.String("type") != "user" || !uuidPattern.MatchString(m.String("uuid")) || json.Unmarshal(m.Get("timestamp"), &timestamp) != nil || string(m.Get("timestamp")) == "null" || json.Unmarshal(m.Get("message"), &payload) != nil || payload.String("role") != "user" {
			return input, errors.New("original ordinary user required")
		}
		var content string
		if json.Unmarshal(payload.Get("content"), &content) != nil || string(payload.Get("content")) == "null" {
			return input, errors.New("original user string required")
		}
	}
	fields := map[string][]string{"switch": {"sid", "projectDir"}, "setOriginalCwd": {"value"}, "setCwdState": {"value"}, "setProjectRoot": {"value"}, "slugSet": {"sid", "slug"}, "slugDelete": {"sid"}, "slugClear": {}, "reset": {}, "record": {"messageIndexes", "awaitRecord"}, "awaitRecord": {}, "flush": {}, "snapshot": {"label"}, "recordWithCwdOverride": {"value", "messageIndexes"}, "recordAtNativeHEAD": {"messageIndexes"}}
	for _, op := range input.Operations {
		name := str(op, "op")
		f, ok := fields[name]
		if !ok || !allowed(op, append([]string{"op"}, f...)...) {
			return input, errors.New("unsupported original operation")
		}
		for _, key := range f {
			if _, ok := op[key]; !ok && key != "projectDir" {
				return input, errors.New("missing original operation field")
			}
		}
		if v, ok := op["projectDir"]; ok && string(v) != "null" {
			if _, e = pathValue(home, v); e != nil {
				return input, e
			}
		}
		if v, ok := op["value"]; ok {
			if _, e = pathValue(home, v); e != nil {
				return input, e
			}
		}
		if _, ok := op["sid"]; ok {
			if str(op, "sid") != "A" && str(op, "sid") != "B" {
				return input, errors.New("original SID reference required")
			}
		}
		for _, key := range []string{"label", "slug"} {
			if v, ok := op[key]; ok {
				var s string
				if json.Unmarshal(v, &s) != nil || string(v) == "null" {
					return input, errors.New("original scalar required")
				}
			}
		}
		if v, ok := op["awaitRecord"]; ok {
			var b bool
			if json.Unmarshal(v, &b) != nil || string(v) == "null" {
				return input, errors.New("original receipt bool required")
			}
		}
		if v, ok := op["messageIndexes"]; ok {
			var indexes []json.RawMessage
			if json.Unmarshal(v, &indexes) != nil || len(indexes) == 0 {
				return input, errors.New("original message indexes required")
			}
			for _, r := range indexes {
				var n int
				if json.Unmarshal(r, &n) != nil || string(r) == "null" || n < 0 || n >= 4 {
					return input, errors.New("original message index required")
				}
			}
		}
	}
	if input.NativeGit != nil {
		n := input.NativeGit
		if input.ID != "native-HEAD-FIFO-after-prepare-live-stamp" || n.Kind != "held-watcher-init-HEAD-FIFO-replaced-with-regular-before-compute-read" || n.HeadBytes != "ref: refs/heads/original-private-branch\n" || n.ControlToken != "original-HEAD-writer-opened" || n.LateSID != "B" || n.LateSlug != "original-late-git-slug" {
			return input, errors.New("original FIFO capability required")
		}
		no, e := objectValue(o["nativeGit"])
		if e != nil || len(no) != 7 || !allowed(no, "kind", "headBytes", "controlToken", "lateSID", "lateSlug", "lateOriginalCwd", "lateCwd") {
			return input, errors.New("original FIFO fields required")
		}
		if _, e = pathValue(home, n.LateOriginalCWD); e != nil {
			return input, e
		}
		if _, e = pathValue(home, n.LateCWD); e != nil {
			return input, e
		}
	} else {
		for _, op := range input.Operations {
			if str(op, "op") == "recordAtNativeHEAD" {
				return input, errors.New("native FIFO operation requires original capability")
			}
		}
	}
	return input, nil
}
func checked(home, path string) error {
	if !filepath.IsAbs(path) {
		return errors.New("absolute private native path required")
	}
	rel, e := filepath.Rel(home, path)
	if e != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return errors.New("native path outside private HOME")
	}
	for current := path; ; current = filepath.Dir(current) {
		_, e := os.Lstat(current)
		if e == nil {
			resolved, e := filepath.EvalSymlinks(current)
			if e != nil {
				return e
			}
			r, e := filepath.Rel(home, resolved)
			if e != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
				return errors.New("private native symlink escape")
			}
			return nil
		}
		if !errors.Is(e, fs.ErrNotExist) {
			return e
		}
		if current == home {
			return e
		}
	}
}

type nativeFiles struct{ home string }

func (f nativeFiles) OpenFile(p string, n int, m fs.FileMode) (sessioncontroller.TranscriptFile, error) {
	if e := checked(f.home, p); e != nil {
		return nil, e
	}
	return sessioncontroller.OSFiles{}.OpenFile(p, n, m)
}
func (f nativeFiles) Stat(p string) (fs.FileInfo, error) {
	if e := checked(f.home, p); e != nil {
		return nil, e
	}
	return os.Stat(p)
}
func (f nativeFiles) MkdirAll(p string, m fs.FileMode) error {
	if e := checked(f.home, p); e != nil {
		return e
	}
	return os.MkdirAll(p, m)
}
func (f nativeFiles) ReadFile(p string) ([]byte, error) {
	if e := checked(f.home, p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}

type loaderFiles struct{ home string }

func (f loaderFiles) ReadFile(p string) ([]byte, error) {
	if e := checked(f.home, p); e != nil {
		return nil, e
	}
	return os.ReadFile(p)
}
func (f loaderFiles) Open(p string) (sessionstore.File, error) {
	if e := checked(f.home, p); e != nil {
		return nil, e
	}
	return sessionstore.OSFileSystem{}.Open(p)
}
func startupHome() (string, error) {
	home := os.Getenv("HOME")
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if home == "" || root == "" || !filepath.IsAbs(home) || !filepath.IsAbs(root) {
		return "", errors.New("explicit disposable HOME required")
	}
	h, e := filepath.EvalSymlinks(home)
	if e != nil {
		return "", e
	}
	r, e := filepath.EvalSymlinks(root)
	if e != nil {
		return "", e
	}
	relative, e := filepath.Rel(r, h)
	if e != nil || relative == "." || strings.HasPrefix(relative, "..") || !strings.HasPrefix(filepath.Base(h), "private-home-") {
		return "", errors.New("private HOME leaf required")
	}
	cwd, e := os.Getwd()
	if e != nil || cwd != h || os.Getenv("USERPROFILE") != h || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(h, ".claude") || os.Getenv("NODE_ENV") != "production" || os.Getenv("TZ") != "UTC" || os.Getenv("CC_HAHA_BOOTSTRAP_PERSISTENCE_FIXTURE") != "1" {
		return "", errors.New("explicit original private environment required")
	}
	for _, name := range []string{"input.json", "result.json", "production-imported.json", "mutation-receipt.json"} {
		info, e := os.Lstat(filepath.Join(h, name))
		if e != nil && !errors.Is(e, fs.ErrNotExist) {
			return "", e
		}
		if e == nil && !info.Mode().IsRegular() {
			return "", errors.New("private input/observer regular file required")
		}
	}
	return h, nil
}
func randomUUID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", e
	}
	b[6] = b[6]&15 | 64
	b[8] = b[8]&63 | 128
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}
func (p *program) error(e error) {
	p.errors = append(p.errors, map[string]any{"name": fmt.Sprintf("%T", e), "message": e.Error(), "stack": string(debug.Stack())})
}
func (p *program) event(name string, data map[string]any) {
	if data == nil {
		data = map[string]any{}
	}
	data["event"] = name
	p.trace = append(p.trace, data)
}
func (p *program) view(scope *cooperative.Scope) (map[string]any, error) {
	s := p.state.Snapshot()
	cache, e := p.state.PlanSlugCache(scope)
	if e != nil {
		return nil, e
	}
	entries, e := cache.Entries(scope)
	if e != nil {
		return nil, e
	}
	plan := make([]any, 0, len(entries))
	for _, v := range entries {
		plan = append(plan, []string{v.SessionID, v.Slug})
	}
	cwd, e := p.cwd.GetCWD(scope)
	if e != nil {
		return nil, e
	}
	computed, e := p.writer.ComputedTranscriptPath(scope)
	if e != nil {
		return nil, e
	}
	return map[string]any{"sessionId": s.SessionID, "sessionProjectDir": s.SessionProjectDir, "originalCwd": s.OriginalCWD, "projectRoot": s.ProjectRoot, "cwdState": s.CWD, "getCwd": cwd, "planEntries": plan, "samePlanMap": p.cache == cache, "computedTranscriptPath": computed}, nil
}
func (p *program) files() ([]any, error) {
	result := []any{}
	e := filepath.WalkDir(filepath.Join(p.home, ".claude"), func(path string, entry fs.DirEntry, e error) error {
		if e != nil {
			return e
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			return errors.New("private observer must not follow symlink")
		}
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".jsonl") {
			return nil
		}
		info, e := entry.Info()
		if e != nil {
			return e
		}
		bytes, e := os.ReadFile(path)
		if e != nil {
			return e
		}
		rel, e := filepath.Rel(p.home, path)
		if e != nil {
			return e
		}
		var ino any
		v := reflect.ValueOf(info.Sys())
		if v.Kind() == reflect.Pointer {
			v = v.Elem()
		}
		if v.Kind() == reflect.Struct {
			f := v.FieldByName("Ino")
			if f.IsValid() && f.CanUint() {
				ino = f.Uint()
			}
		}
		result = append(result, map[string]any{"path": path, "relativePath": rel, "bytes": string(bytes), "mode": uint32(info.Mode().Perm()), "mtimeMs": float64(info.ModTime().UnixNano()) / 1e6, "mtimeNs": fmt.Sprint(info.ModTime().UnixNano()), "inode": ino, "size": info.Size()})
		return nil
	})
	return result, e
}
func (p *program) snapshot(scope *cooperative.Scope, label string) error {
	state, e := p.view(scope)
	if e != nil {
		return e
	}
	files, e := p.files()
	if e != nil {
		return e
	}
	value := map[string]any{"label": label, "state": state, "files": files}
	p.snapshots = append(p.snapshots, value)
	p.event("snapshot", map[string]any{"label": label, "state": state, "files": files})
	return nil
}
func (p *program) record(scope *cooperative.Scope, op object, cwd *bootstrapstate.CWDContext) (*sessioncontroller.RecordFuture, error) {
	var indexes []int
	if e := json.Unmarshal(op["messageIndexes"], &indexes); e != nil {
		return nil, e
	}
	messages := make([]sessioncontroller.LiveRecord, 0, len(indexes))
	for _, n := range indexes {
		messages = append(messages, sessioncontroller.LiveRecord{Record: &p.input.Messages[n]})
	}
	return p.pending.BeginRecordLive(scope, p.loader, sessioncontroller.LiveRecordProfile{QuerySource: "sdk"}, messages, sessioncontroller.RecordOptions{PromptID: func() json.RawMessage { return quote("original-prompt-id") }, CWDContext: cwd, Branch: func(next *cooperative.Scope) (sessioncontroller.RecordBranchFuture, error) {
		return p.git.BranchWithCWD(next, func(fresh *cooperative.Scope) (string, error) { return cwd.GetCWD(fresh) })
	}})
}
func (p *program) awaitRecord(scope *cooperative.Scope) error {
	if p.lastRecord == nil {
		return errors.New("original pending receipt required")
	}
	result, e := p.lastRecord.Await(scope)
	if e != nil {
		return e
	}
	state, e := p.view(scope)
	if e != nil {
		return e
	}
	p.event("record:awaited", map[string]any{"result": result, "state": state})
	p.lastRecord = nil
	return nil
}
func (p *program) execute(scope *cooperative.Scope, op object) error {
	p.event("operation:start", map[string]any{"original": op})
	name := str(op, "op")
	switch name {
	case "switch":
		var projectDir *string
		if v, ok := op["projectDir"]; ok && string(v) != "null" {
			dir, e := pathValue(p.home, v)
			if e != nil {
				return e
			}
			projectDir = &dir
		}
		if e := p.state.SwitchSession(scope, p.input.SessionIDs[str(op, "sid")], projectDir); e != nil {
			return e
		}
	case "setOriginalCwd", "setCwdState", "setProjectRoot":
		v, e := pathValue(p.home, op["value"])
		if e != nil {
			return e
		}
		switch name {
		case "setOriginalCwd":
			e = p.state.SetOriginalCWD(scope, v)
		case "setCwdState":
			e = p.state.SetCWD(scope, v)
		case "setProjectRoot":
			e = p.state.SetProjectRoot(scope, v)
		}
		if e != nil {
			return e
		}
	case "slugSet":
		if e := p.cache.Set(scope, p.input.SessionIDs[str(op, "sid")], str(op, "slug")); e != nil {
			return e
		}
	case "slugDelete":
		deleted, e := p.cache.Delete(scope, p.input.SessionIDs[str(op, "sid")])
		if e != nil {
			return e
		}
		p.event("slugDelete:return", map[string]any{"deleted": deleted})
	case "slugClear":
		if e := p.cache.Clear(scope); e != nil {
			return e
		}
	case "reset":
		future, e := p.writer.BeginResetSessionFile(scope)
		if e != nil {
			return e
		}
		if _, e = future.Await(scope); e != nil {
			return e
		}
	case "record", "recordAtNativeHEAD":
		if p.lastRecord != nil {
			return errors.New("previous original Record receipt must be consumed")
		}
		future, e := p.record(scope, op, p.cwd)
		if e != nil {
			return e
		}
		p.lastRecord = future
		state, e := p.view(scope)
		if e != nil {
			return e
		}
		p.event("record:returned", map[string]any{"state": state})
		if name == "recordAtNativeHEAD" {
			token, e := scope.AwaitBytes(cooperative.SDKOutput, func(context.Context) ([]byte, error) { return io.ReadAll(os.Stdin) })
			if e != nil {
				return e
			}
			if string(token) != p.input.NativeGit.ControlToken {
				return errors.New("original native OS input token required")
			}
			before, e := p.view(scope)
			if e != nil {
				return e
			}
			p.event("native-HEAD-control:received", map[string]any{"originalToken": string(token), "stateBefore": before})
			n := p.input.NativeGit
			if e = p.state.SwitchSession(scope, p.input.SessionIDs[n.LateSID], nil); e != nil {
				return e
			}
			if e = p.cache.Set(scope, p.state.SessionID(), n.LateSlug); e != nil {
				return e
			}
			value, e := pathValue(p.home, n.LateOriginalCWD)
			if e != nil {
				return e
			}
			if e = p.state.SetOriginalCWD(scope, value); e != nil {
				return e
			}
			value, e = pathValue(p.home, n.LateCWD)
			if e != nil {
				return e
			}
			if e = p.state.SetCWD(scope, value); e != nil {
				return e
			}
			state, e := p.view(scope)
			if e != nil {
				return e
			}
			bytes, e := json.Marshal(map[string]any{"state": state, "originalToken": string(token)})
			if e != nil {
				return e
			}
			if e = os.WriteFile(filepath.Join(p.home, "mutation-receipt.json"), bytes, 0600); e != nil {
				return e
			}
			p.event("native-HEAD-mutations:returned", map[string]any{"state": state})
			if e = p.awaitRecord(scope); e != nil {
				return e
			}
		} else {
			var wait bool
			_ = json.Unmarshal(op["awaitRecord"], &wait)
			if wait {
				if e = p.awaitRecord(scope); e != nil {
					return e
				}
			}
		}
	case "awaitRecord":
		if e := p.awaitRecord(scope); e != nil {
			return e
		}
	case "recordWithCwdOverride":
		v, e := pathValue(p.home, op["value"])
		if e != nil {
			return e
		}
		var result json.RawMessage
		e = p.cwd.RunWithOverride(scope, v, func(cwd *bootstrapstate.CWDContext) error {
			future, e := p.record(scope, op, cwd)
			if e != nil {
				return e
			}
			result, e = future.Await(scope)
			return e
		})
		if e != nil {
			return e
		}
		state, e := p.view(scope)
		if e != nil {
			return e
		}
		p.event("recordWithCwdOverride:awaited", map[string]any{"result": result, "override": v, "stateAfterOverride": state})
	case "flush":
		future, e := p.pending.Flush(scope)
		if e != nil {
			return e
		}
		if e = future.Await(scope); e != nil {
			return e
		}
		files, e := p.files()
		if e != nil {
			return e
		}
		p.event("flush:awaited", map[string]any{"files": files})
	case "snapshot":
		if e := p.snapshot(scope, str(op, "label")); e != nil {
			return e
		}
	default:
		return errors.New("unsupported original operation")
	}
	state, e := p.view(scope)
	if e != nil {
		return e
	}
	p.event("operation:return", map[string]any{"original": op, "state": state})
	return nil
}
func run() error {
	home, e := startupHome()
	if e != nil {
		return e
	}
	raw, e := os.ReadFile(filepath.Join(home, "input.json"))
	if e != nil {
		return e
	}
	input, e := validate(home, raw)
	if e != nil {
		return e
	}
	owner := cooperative.New()
	defer owner.Close()
	id, e := randomUUID()
	if e != nil {
		return e
	}
	state, e := bootstrapstate.New(owner, id, home, randomUUID)
	if e != nil {
		return e
	}
	p := &program{home: home, input: input, state: state, trace: []any{}, snapshots: []any{}, errors: []any{}, absentPaths: []string{}}
	absent := map[string]bool{}
	for dir := filepath.Dir(home); ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, ".git")
		p.absentPaths = append(p.absentPaths, path)
		absent[path] = true
		if filepath.Dir(dir) == dir {
			break
		}
	}
	settings, e := clisettings.NewState(func() float64 { return float64(time.Now().UnixMilli()) })
	if e != nil {
		return e
	}
	settingService, e := clisettings.NewService(clisettings.Options{State: settings, ConfigHome: filepath.Join(home, ".claude"), OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return float64(time.Now().UnixMilli()) }, PID: os.Getpid()})
	if e != nil {
		return e
	}
	configDir := filepath.Join(home, ".claude")
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &configDir})
	if e != nil {
		return e
	}
	defer global.Close()
	env, e := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(configDir)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	if e != nil {
		return e
	}
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settingService, SettingsState: settings, Environment: env, ConfigHome: configDir, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		return e
	}
	if _, e = startup.GetInitialSettings(); e != nil {
		return e
	}
	files := nativeFiles{home}
	p.writer, e = sessioncontroller.NewWriter(sessioncontroller.WriterOptions{Bootstrap: state, ConfigDir: configDir, CWD: home, SessionID: id, Version: "fixture-version", Environment: map[string]string{"NODE_ENV": "production", "CLAUDE_CODE_SIMPLE": "1"}, SettingsState: settings, Files: files, Branch: func() (string, error) {
		return "", &sessioncontroller.CapabilityError{Boundary: "original fixture requires real same-owner Git Branch future"}
	}, OnError: p.error, Schedule: sessioncontroller.RealSchedule, Now: time.Now})
	if e != nil {
		return e
	}
	p.pending, e = sessioncontroller.NewPendingWriter(p.writer, owner)
	if e != nil {
		return e
	}
	defer p.pending.Close(context.Background())
	lf := loaderFiles{home}
	p.loader, e = sessionhistory.New(sessionhistory.Options{Files: lf, Stat: func(path string) (int64, error) {
		if e := checked(home, path); e != nil {
			return 0, e
		}
		info, e := os.Stat(path)
		if e != nil {
			return 0, e
		}
		return info.Size(), nil
	}, ReadFile: lf.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(v sessionhistory.Diagnostic) {
		p.errors = append(p.errors, map[string]any{"code": v.Code, "message": v.Message, "fields": v.Fields})
	}})
	if e != nil {
		return e
	}
	stat := func(path string) (fs.FileInfo, error) {
		if absent[path] {
			return nil, fs.ErrNotExist
		}
		if e := checked(home, path); e != nil {
			return nil, e
		}
		return os.Stat(path)
	}
	read := func(path string) ([]byte, error) {
		if e := checked(home, path); e != nil {
			return nil, e
		}
		return os.ReadFile(path)
	}
	p.watch = &gitbranch.NativePollWatch{Stat: stat}
	p.git, e = gitbranch.New(gitbranch.Options{Owner: owner, CWD: func() string { return state.Snapshot().CWD }, Now: func() int64 { return time.Now().UnixMilli() }, StatSync: stat, Stat: stat, ReadFile: read, WatchFile: p.watch.Watch, ScrollDraining: func() bool { return false }, RegisterCleanup: func(callback func(*cooperative.Scope) error) {
		p.cleanupCallbacks = append(p.cleanupCallbacks, callback)
	}, NodeEnv: "production"})
	if e != nil {
		return e
	}
	defer func() { _ = p.git.Close(context.Background()); p.watch.Join() }()
	marker, _ := json.Marshal(map[string]any{"imported": true, "id": input.ID})
	if e = os.WriteFile(filepath.Join(home, "production-imported.json"), marker, 0600); e != nil {
		return e
	}
	var result []byte
	e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		var e error
		p.cwd, e = state.Context(scope)
		if e != nil {
			return e
		}
		p.cache, e = state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		if e = p.snapshot(scope, "initial"); e != nil {
			return e
		}
		for _, op := range input.Operations {
			if e = p.execute(scope, op); e != nil {
				return e
			}
		}
		if p.lastRecord != nil {
			return errors.New("original program left unconsumed receipt")
		}
		if e = p.snapshot(scope, "program-end"); e != nil {
			return e
		}
		if input.NativeGit != nil {
			if e = p.git.Reset(scope); e != nil {
				return e
			}
			p.event("original-git-watcher-reset:returned", nil)
		}
		state, e := p.view(scope)
		if e != nil {
			return e
		}
		files, e := p.files()
		if e != nil {
			return e
		}
		result, e = json.Marshal(map[string]any{"input": json.RawMessage(raw), "home": home, "actualPid": os.Getpid(), "trace": p.trace, "snapshots": p.snapshots, "files": files, "finalState": state, "capabilities": map[string]any{"blocked": []any{}, "secureReads": []any{}, "absentReads": []any{}}, "exactAbsentPaths": p.absentPaths, "errors": p.errors, "scope": "Actual private Go Record/reset/flush with same owner live state/cache/CWD context and physical JSONL; no CLI or force-exit proof"})
		return e
	})
	if e != nil {
		return e
	}
	if e = os.WriteFile(filepath.Join(home, "result.json"), result, 0600); e != nil {
		return e
	}
	admin, _ := json.Marshal(map[string]any{"resultCapturedBeforeAdministrativeGitAndPendingClose": true, "singleOwner": true, "settings": "Independent original private absent files through shared settings/getInitialSettings; Source internal init/config side effects not ported by this fixture", "GitWatcher": "real finite native poll adapter; universal Bun scheduling not claimed", "retainedOriginalCleanupCallbacks": len(p.cleanupCallbacks), "cleanupRegistryScope": "Retains actual callable registrations on the canonical owner; original program does not run cleanup; full runtime registry registration phase and natural process drain not proved"})
	return os.WriteFile(filepath.Join(home, "go-administrative-observation.json"), admin, 0600)
}
func main() {
	if e := run(); e != nil {
		fmt.Fprintln(os.Stderr, e)
		os.Exit(1)
	}
}
