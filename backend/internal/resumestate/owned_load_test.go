package resumestate

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/bootstrapstate"
	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistorycopy"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

type publicSink struct {
	binding *ownerasync.NativeBinding
	owner   *cooperative.Owner
	calls   int
}

func (s *publicSink) NativeCopyBinding() *ownerasync.NativeBinding { return s.binding }
func (s *publicSink) BeginFileHistorySnapshot(scope *cooperative.Scope, _ json.RawMessage, _ *protocol.JSONObject, _ bool) (*ownerasync.OwnerFuture[struct{}], error) {
	s.calls++
	f, _ := ownerasync.NewOwnerFuture[struct{}](s.owner)
	_ = f.Resolve(scope, struct{}{})
	return f, nil
}
func publicService(t *testing.T, files Files) (*Service, *cooperative.Owner, *memoryEvents) {
	s, o, e, _ := publicServiceReferences(t, files)
	return s, o, e
}
func publicServiceReferences(t *testing.T, files Files) (*Service, *cooperative.Owner, *memoryEvents, *ownerasync.NativeReferences) {
	t.Helper()
	home := t.TempDir()
	options, events := testOptions(t, home, files)
	owner := cooperative.New()
	t.Cleanup(owner.Close)
	state, e := bootstrapstate.New(owner, options.CurrentSessionID, home, func() (string, error) { return tempSID, nil })
	if e != nil {
		t.Fatal(e)
	}
	config := options.ConfigDir
	global, e := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config})
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { _ = global.Close() })
	settings, e := clisettings.NewService(clisettings.Options{State: options.SettingsState, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, Now: func() float64 { return 1700000000000 }, PID: os.Getpid()})
	if e != nil {
		t.Fatal(e)
	}
	env, _ := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
	startup, e := clistartupenv.New(clistartupenv.Options{Global: global, Settings: settings, SettingsState: options.SettingsState, Environment: env, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
	if e != nil {
		t.Fatal(e)
	}
	service, e := NewOwned(options, state, startup)
	if e != nil {
		t.Fatal(e)
	}
	refs := &ownerasync.NativeReferences{}
	binding, e := refs.Bind(owner)
	if e != nil {
		t.Fatal(e)
	}
	sink := &publicSink{binding: binding, owner: owner}
	copy, e := filehistorycopy.New(filehistorycopy.Options{Owner: owner, Binding: binding, Sink: sink, Files: files, Environment: func(k string) string { return options.Environment[k] }, NonInteractive: func() bool { return true }, ConfigHome: func() string { return config }, SessionID: state.SessionID, OnError: events.onError, Debug: func(string) {}, Event: func(string, protocol.JSONObject) {}})
	if e != nil {
		t.Fatal(e)
	}
	if e = service.BindOwnedFileHistory(copy); e != nil {
		t.Fatal(e)
	}
	if e = service.BindOwnedNative(binding, func(*cooperative.Scope, string, *sessionhistory.Transcript) error { return nil }); e != nil {
		t.Fatal(e)
	}
	return service, owner, events, refs
}
func TestOwnedPublicNoSlugAndBareKeepActualProducerReactions(t *testing.T) {
	s, o, _ := publicService(t, OSFiles{})
	order := []string{}
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := original([]json.RawMessage{plain("input", "original")})
		f, e := s.BeginLoad(scope, input)
		if e != nil {
			return e
		}
		select {
		case <-f.Done():
			t.Error("public body skipped plan/hook await")
		default:
		}
		order = append(order, "start-return")
		_, e = o.Microtask(cooperative.Read, func(*cooperative.Scope) error { order = append(order, "queued-after-plan-await"); return nil })
		if e != nil {
			return e
		}
		result, e := f.Await(scope)
		if e != nil {
			return e
		}
		order = append(order, "public-return")
		if result == nil || len(result.Messages) != 2 || result.TurnInterruptionState.Kind != "interrupted_prompt" {
			t.Errorf("real reducer not used: %#v", result)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if len(order) != 3 || order[1] != "queued-after-plan-await" {
		t.Fatal(order)
	}
}
func TestOwnedPublicNoEagerSettingsAndBareUsesLiveFlag(t *testing.T) {
	s, o, _ := publicService(t, OSFiles{})
	writePrivate(t, filepath.Join(s.options.ConfigDir, "settings.json"), `{"future":{"bad":true}}`)
	if s.options.SettingsState.GetSessionSettingsCache() != nil {
		t.Fatal("constructor eagerly seeded settings")
	}
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.BeginLoad(scope, original([]json.RawMessage{plain("input", "original")}))
		if e != nil {
			return e
		}
		if _, e = f.Await(scope); e != nil {
			return e
		}
		delete(s.options.Environment, "CLAUDE_CODE_SIMPLE")
		f, e = s.BeginLoad(scope, original([]json.RawMessage{}))
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			t.Errorf("nonbare escaped actual SessionStart gate: %v", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if s.options.SettingsState.GetSessionSettingsCache() != nil {
		t.Fatal("bare no-plan loaded unrelated settings")
	}
}
func TestOwnedPublicCapabilitiesAndAdmissionAreSeparate(t *testing.T) {
	s, o, _ := publicService(t, OSFiles{})
	foreign := cooperative.New()
	defer foreign.Close()
	if e := foreign.Run(context.Background(), func(scope *cooperative.Scope) error { _, e := s.BeginLoad(scope, Input{}); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if _, e := s.BeginLoad(nil, Input{}); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, input := range []Input{{}, {Source: quote(oldSID)}, {Source: []byte(`{"messages":[],"sessionId":"x","fullPath":"/unsupported"}`)}, {Source: []byte(`[]`)}, {Source: []byte(`{"messages":3}`)}, {Source: []byte(`{"messages":[],"fileHistorySnapshots":[3]}`)}, {Original: &OriginalLog{}, Source: []byte("null")}} {
			f, e := s.BeginLoad(scope, input)
			if e != nil {
				t.Errorf("async body threw at admission: %v", e)
				continue
			}
			if _, e = f.Await(scope); e == nil {
				t.Error("unported input silently resolved")
			}
		}
		f, e := s.BeginLoad(scope, Input{Source: []byte("null")})
		if e != nil {
			return e
		}
		v, e := f.Await(scope)
		if e != nil || v != nil {
			t.Error(v, e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestOwnedPublicResultKeepsSnapshotArrayIdentity(t *testing.T) {
	s, o, _ := publicService(t, OSFiles{})
	fields := object([]byte(`{"messages":[],"sessionId":"x","fileHistorySnapshots":[]}`))
	log, e := DecodeOriginalLog(fields)
	if e != nil {
		t.Fatal(e)
	}
	if e = o.Run(context.Background(), func(scope *cooperative.Scope) error {
		old := log.History.Snapshots
		f, e := s.BeginLoad(scope, Input{Original: log})
		if e != nil {
			return e
		}
		replacement := &filehistorycopy.SnapshotArray{Items: []*protocol.JSONObject{}}
		log.ReplaceSnapshots(replacement)
		v, e := f.Await(scope)
		if e != nil {
			return e
		}
		if v.ownedSnapshots != replacement || old == replacement {
			t.Error("public return retained copy-time metadata clone")
		}
		item := object([]byte(`{"messageId":"live"}`))
		replacement.Items = append(replacement.Items, &item)
		raw, e := v.MarshalJSON()
		if e != nil {
			return e
		}
		if string(object(raw).Get("fileHistorySnapshots")) != `[{"messageId":"live"}]` {
			t.Errorf("late observer eagerly serialized snapshot: %s", raw)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}
func TestOwnedPublicSkillsRefusesDisconnectedAuthority(t *testing.T) {
	s, o, _ := publicService(t, OSFiles{})
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		input := original([]json.RawMessage{[]byte(`{"type":"attachment","attachment":{"type":"skill_listing","content":"","skills":[]}}`)})
		f, e := s.BeginLoad(scope, input)
		if e != nil {
			return e
		}
		_, e = f.Await(scope)
		var cap *CapabilityError
		if !errors.As(e, &cap) {
			t.Error("isolated resume latch falsely claimed shared state", e)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

type heldPlan struct {
	OSFiles
	entered chan struct{}
	release chan struct{}
	once    sync.Once
}

func (f *heldPlan) ReadFile(path string) ([]byte, error) {
	f.once.Do(func() { close(f.entered) })
	<-f.release
	return f.OSFiles.ReadFile(path)
}
func TestOwnedPublicRealPlanReadCapturesArrayAtAfterPlanBoundary(t *testing.T) {
	files := &heldPlan{entered: make(chan struct{}), release: make(chan struct{})}
	s, o, _ := publicService(t, files)
	writePrivate(t, filepath.Join(s.options.ConfigDir, "plans", "original.md"), "plan")
	log, e := DecodeOriginalLog(object([]byte(`{"messages":[{"type":"user","slug":"original","sessionId":"before","message":{"role":"user","content":"x"}}],"fileHistorySnapshots":[]}`)))
	if e != nil {
		t.Fatal(e)
	}
	if e = o.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.BeginLoad(scope, Input{Original: log})
		if e != nil {
			return e
		}
		if e = scope.AwaitPromise(files.entered); e != nil {
			return e
		}
		old := log.History.Snapshots
		item := object([]byte(`{"messageId":"new"}`))
		fresh := &filehistorycopy.SnapshotArray{Items: []*protocol.JSONObject{&item}}
		log.ReplaceSnapshots(fresh)
		close(files.release)
		v, e := f.Await(scope)
		if e != nil {
			return e
		}
		cache, e := s.owned.state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		slug, ok, e := cache.Get(scope, "before")
		if e != nil {
			return e
		}
		if !ok || slug != "original" || v.ownedSnapshots != fresh || old == fresh {
			t.Error("plan/shared cache/reference wrong")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

type failingPlan struct {
	OSFiles
	failure error
}

func (f failingPlan) ReadFile(string) ([]byte, error) { return nil, f.failure }
func TestOwnedPublicPlanErrorsAreCaughtAndRemoteRecoveryIsCapability(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "native-permission", true: "remote-missing"}[missing], func(t *testing.T) {
			reason := errors.New("original native permission object")
			if missing {
				reason = fs.ErrNotExist
			}
			s, o, events := publicService(t, failingPlan{failure: reason})
			if missing {
				s.options.EnvironmentKind = "byoc"
			}
			if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
				f, e := s.BeginLoad(scope, original([]json.RawMessage{plain("u", "x")}, protocol.JSONField{Name: "sessionId", Value: quote("old")}))
				// no slug is the fulfilled async fast branch, independent of remote kind.
				if e != nil {
					return e
				}
				if _, e = f.Await(scope); e != nil {
					return e
				}
				m := object(plain("u", "x"))
				m.Set("slug", quote("original"))
				f, e = s.BeginLoad(scope, original([]json.RawMessage{encode(m)}))
				if e != nil {
					return e
				}
				_, e = f.Await(scope)
				if missing {
					var cap *CapabilityError
					if !errors.As(e, &cap) {
						t.Errorf("remote fallback approximated: %v", e)
					}
				} else if e != nil {
					t.Error("caught native read failed public load", e)
				}
				return nil
			}); e != nil {
				t.Fatal(e)
			}
			if len(events.errors) != 1 {
				t.Fatal("plan/public diagnostic catch changed", events.errors)
			}
		})
	}
}
func TestOwnedPublicUnpairedPlanLeavesRejectBeforeCacheAndFilesystem(t *testing.T) {
	s, o, _ := publicService(t, OSFiles{})
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		for _, raw := range []string{`{"messages":[{"type":"user","sessionId":"old","slug":"\ud800","message":{"role":"user","content":"x"}}]}`, `{"messages":[{"type":"user","sessionId":"\udc00","slug":"safe","message":{"role":"user","content":"x"}}]}`} {
			f, e := s.BeginLoad(scope, Input{Source: []byte(raw)})
			if e != nil {
				return e
			}
			_, e = f.Await(scope)
			var cap *CapabilityError
			if !errors.As(e, &cap) {
				t.Error("unpaired leaf replaced", e)
			}
		}
		cache, e := s.owned.state.PlanSlugCache(scope)
		if e != nil {
			return e
		}
		entries, e := cache.Entries(scope)
		if e != nil {
			return e
		}
		if len(entries) != 0 {
			t.Error("unsupported leaves touched cache")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	if _, e := os.Stat(filepath.Join(s.options.ConfigDir, "plans")); !errors.Is(e, fs.ErrNotExist) {
		t.Error("unsupported leaves touched filesystem", e)
	}
}

type heldCopyDirectory struct {
	OSFiles
	entered, release, finished chan struct{}
	once                       sync.Once
}

func (f *heldCopyDirectory) MkdirAll(path string) error {
	if filepath.Base(filepath.Dir(path)) == "file-history" {
		f.once.Do(func() { close(f.entered) })
		<-f.release
		defer close(f.finished)
	}
	return f.OSFiles.MkdirAll(path)
}
func TestOwnedPublicReturnDoesNotJoinActualVoidCopyNativeJob(t *testing.T) {
	files := &heldCopyDirectory{entered: make(chan struct{}), release: make(chan struct{}), finished: make(chan struct{})}
	s, o, _, refs := publicServiceReferences(t, files)
	s.options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = "1"
	released := false
	defer func() {
		if !released {
			close(files.release)
		}
		<-files.finished
		_, idle := refs.Snapshot()
		if idle != nil {
			<-idle
		}
	}()
	log := object([]byte(`{"messages":[{"type":"user","sessionId":"previous","message":{"role":"user","content":"x"}}],"fileHistorySnapshots":[{"messageId":"snap","trackedFileBackups":{}}]}`))
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		f, e := s.BeginLoad(scope, Input{Source: encode(log)})
		if e != nil {
			return e
		}
		if e = scope.AwaitPromise(files.entered); e != nil {
			return e
		}

		v, e := f.Await(scope)
		if e != nil {
			return e
		}
		if v == nil {
			t.Error("public result missing")
		}
		count, _ := refs.Snapshot()
		if count != 1 {
			t.Error("pure Promise/native reference distinction lost", count)
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	close(files.release)
	released = true
	// Purely administrative native cleanup after all public assertions.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	select {
	case <-files.finished:
	case <-ctx.Done():
		t.Fatal("native job failed to return")
	}
}

type closingHeldPlan struct {
	heldPlan
	finished chan struct{}
}

func (f *closingHeldPlan) ReadFile(path string) ([]byte, error) {
	defer close(f.finished)
	return f.heldPlan.ReadFile(path)
}
func TestOwnedPublicOwnerCloseSettlesPromiseWithoutOffOwnerReducer(t *testing.T) {
	files := &closingHeldPlan{heldPlan: heldPlan{entered: make(chan struct{}), release: make(chan struct{})}, finished: make(chan struct{})}
	s, o, events, refs := publicServiceReferences(t, files)
	var public *ownerasync.OwnerFuture[*Result]
	if e := o.Run(context.Background(), func(scope *cooperative.Scope) error {
		m := object(plain("u", "x"))
		m.Set("slug", quote("held"))
		var e error
		public, e = s.BeginLoad(scope, original([]json.RawMessage{encode(m)}))
		if e != nil {
			return e
		}
		return scope.AwaitPromise(files.entered)
	}); e != nil {
		t.Fatal(e)
	}
	count, idle := refs.Snapshot()
	if count != 1 {
		t.Fatal("native read not referenced", count)
	}
	o.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e := public.Wait(ctx); !errors.Is(e, cooperative.ErrClosed) {
		t.Error("unsettled administrative obligation", e)
	}
	close(files.release)
	select {
	case <-files.finished:
	case <-ctx.Done():
		t.Fatal("physical read did not return")
	}
	select {
	case <-idle:
	case <-ctx.Done():
		t.Fatal("failed delivery leaked actual native ref")
	}
	if len(events.errors) != 0 || len(events.diagnostics) != 0 {
		t.Error("business reducer/diagnostics ran offOwner after close", events.errors, events.diagnostics)
	}
}
func TestOwnedPublicCallerWaitCancelDoesNotCancelAdmittedPlan(t *testing.T) {
	files := &heldPlan{entered: make(chan struct{}), release: make(chan struct{})}
	s, o, _, refs := publicServiceReferences(t, files)
	ctx, cancel := context.WithCancelCause(context.Background())
	reason := errors.New("caller wait administrative reason")
	var public *ownerasync.OwnerFuture[*Result]
	e := o.Run(ctx, func(scope *cooperative.Scope) error {
		m := object(plain("u", "x"))
		m.Set("slug", quote("held"))
		var e error
		public, e = s.BeginLoad(scope, original([]json.RawMessage{encode(m)}))
		if e != nil {
			return e
		}
		if e = scope.AwaitPromise(files.entered); e != nil {
			return e
		}
		cancel(reason)
		_, e = public.Await(scope)
		return e
	})
	if e != reason {
		t.Error("caller reason transformed into producer failure", e)
	}
	select {
	case <-public.Done():
		t.Error("signal-free producer cancelled by observer")
	default:
	}
	close(files.release)
	if e = o.Run(context.Background(), func(scope *cooperative.Scope) error {
		v, e := public.Await(scope)
		if v == nil {
			t.Error("admitted load did not finish")
		}
		return e
	}); e != nil {
		t.Fatal(e)
	}
	_, idle := refs.Snapshot()
	if idle != nil {
		<-idle
	}
}
