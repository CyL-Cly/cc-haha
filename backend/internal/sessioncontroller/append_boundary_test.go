package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAppendAdmissionAndClosureCapabilities(t *testing.T) {
	owner, _, p, loader := uuidHarness(t, uuidOld, nil, false)
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"user"`)}, protocol.JSONField{Name: "uuid", Value: json.RawMessage(`"new"`)})
	if _, e := p.BeginAppend(nil, loader, &entry, ""); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	other := cooperative.New()
	defer other.Close()
	if e := other.Run(context.Background(), func(s *cooperative.Scope) error { _, e := p.BeginAppend(s, loader, &entry, ""); return e }); !errors.Is(e, cooperative.ErrRestricted) {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		for _, v := range []struct {
			l *sessionhistory.Loader
			e *protocol.JSONObject
		}{{nil, &entry}, {loader, nil}} {
			if _, e := p.BeginAppend(s, v.l, v.e, ""); e == nil {
				return ioUnexpected()
			}
		}
		if _, e := p.BeginAppend(s, loader, &entry, "other"); e == nil {
			return ioUnexpected()
		}
		for _, raw := range []json.RawMessage{json.RawMessage(`3`), json.RawMessage(`null`), json.RawMessage(`{}`), json.RawMessage(`[]`)} {
			bad := entry.Clone()
			bad.Set("uuid", raw)
			if _, e := p.BeginAppend(s, loader, &bad, ""); e == nil {
				return ioUnexpected()
			}
		}
		for _, kind := range []string{"user", "content-replacement"} {
			bad := entry.Clone()
			bad.Set("type", quote(kind))
			bad.Set("agentId", quote("original-agent"))
			bad.Set("isSidechain", json.RawMessage(`true`))
			if _, e := p.BeginAppend(s, loader, &bad, ""); e == nil {
				return ioUnexpected()
			}
		}
		return nil
	})
	if e := p.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		_, e := p.BeginAppend(s, loader, &entry, "")
		if !errors.Is(e, ErrPendingClosed) {
			return ioUnexpected()
		}
		return nil
	})
}
func TestAppendCacheOwnershipFailureIsNotAnotherLoader(t *testing.T) {
	owner, w, p, loader := uuidHarness(t, uuidOld, nil, false)
	w.AdoptSessionFile()
	f := uuidBegin(t, owner, p, loader, w.options.SessionID)
	if _, e := f.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	_, _, _, foreign := uuidHarness(t, uuidOld, nil, false)
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote("new")})
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		future, e := p.BeginAppend(s, foreign, &entry, "")
		if e == nil || future != nil {
			return ioUnexpected()
		}
		return nil
	})
}
func TestAppendObserverCancellationKeepsOneSetAndRealIO(t *testing.T) {
	owner := cooperative.New()
	fs := &appendIO{armed: true, hold: "open", entered: make(chan struct{}), released: make(chan struct{}), counts: map[string]int{}}
	w := actualPendingWriter(t, owner, t.TempDir(), fs)
	// Construct the existing target without invoking a second Writer/prepare.
	if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(w.target(), []byte(uuidOld), 0600); e != nil {
		t.Fatal(e)
	}
	var p *PendingWriter
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		var e error
		p, e = NewPendingWriter(w, owner)
		w.AdoptSessionFile()
		return e
	})
	t.Cleanup(func() { _ = p.Close(context.Background()); owner.Close() })
	loader := appendBoundaryLoader(t, fs)
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote("new")})
	future := beginAppend(t, owner, p, loader, &entry)
	awaitSignal(t, fs.entered, "real open")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if e := future.Wait(ctx); !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { entry.Set("future", json.RawMessage(`{"late":true}`)); return nil })
	close(fs.released)
	if e := future.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	f := uuidBegin(t, owner, p, loader, w.options.SessionID)
	set, e := f.Wait(context.Background())
	if e != nil {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		found, e := set.OwnerHas(s, quote("new"))
		if e != nil || !found {
			return ioUnexpected()
		}
		return nil
	})
	if found, _ := w.Has(quote("new")); found {
		t.Fatal("append dual-wrote legacy Writer.members")
	}
	var flush *FlushFuture
	pendingCall(t, owner, cooperative.Flush, func(s *cooperative.Scope) error { flush, e = p.Flush(s); return e })
	if e = flush.Wait(context.Background()); e != nil {
		t.Fatal(e)
	}
	b, e := os.ReadFile(w.target())
	if e != nil || !bytes.Contains(b, []byte(`"future":{"late":true}`)) {
		t.Fatal(string(b), e)
	}
}
func TestAppendAdministrativeOwnerStopClosesUnadoptedLease(t *testing.T) {
	owner := cooperative.New()
	fs := &appendIO{armed: true, hold: "open", entered: make(chan struct{}), released: make(chan struct{}), counts: map[string]int{}}
	w := actualPendingWriter(t, owner, t.TempDir(), fs)
	if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(w.target(), nil, 0600); e != nil {
		t.Fatal(e)
	}
	var p *PendingWriter
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		var e error
		p, e = NewPendingWriter(w, owner)
		w.AdoptSessionFile()
		return e
	})
	loader := appendBoundaryLoader(t, fs)
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("summary")})
	future := beginAppend(t, owner, p, loader, &entry)
	awaitSignal(t, fs.entered, "ownerstop actualopen")
	owner.Close()
	close(fs.released)
	if e := future.Wait(context.Background()); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal(e)
	}
	p.workers.Wait()
	fs.mu.Lock()
	closed := fs.counts["close"]
	fs.mu.Unlock()
	if closed != 1 {
		t.Fatal("unused native lease leaked", closed)
	}
}

func appendBoundaryLoader(t *testing.T, fs *appendIO) *sessionhistory.Loader {
	t.Helper()
	loader, e := sessionhistory.New(sessionhistory.Options{Files: fs, Stat: fs.StatSize, ReadFile: fs.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if e != nil {
		t.Fatal(e)
	}
	return loader
}

func TestAppendLateReferenceUUIDRejectedAfterActualColdRead(t *testing.T) {
	owner := cooperative.New()
	io := &appendIO{armed: true, hold: "readFile", entered: make(chan struct{}), released: make(chan struct{}), counts: map[string]int{}}
	w := actualPendingWriter(t, owner, t.TempDir(), io)
	if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(w.target(), []byte(uuidOld), 0600); e != nil {
		t.Fatal(e)
	}
	var p *PendingWriter
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		var e error
		p, e = NewPendingWriter(w, owner)
		w.AdoptSessionFile()
		return e
	})
	t.Cleanup(func() { _ = p.Close(context.Background()); owner.Close() })
	loader := appendBoundaryLoader(t, io)
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("user")}, protocol.JSONField{Name: "uuid", Value: quote("new")})
	future := beginAppend(t, owner, p, loader, &entry)
	awaitSignal(t, io.entered, "native cache read complete")
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { entry.Set("uuid", json.RawMessage(`{"reference":true}`)); return nil })
	close(io.released)
	if e := future.Wait(context.Background()); !errors.Is(e, sessionhistory.ErrUnsupportedMessageKey) {
		t.Fatal(e)
	}
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
		state, e := appendSnapshot(s, p, w.options.SessionID)
		if e != nil {
			return e
		}
		if state["tracked"].(int) != 0 || len(state["queued"].([]string)) != 0 {
			return ioUnexpected()
		}
		return nil
	})
}

type appendNonregularFiles struct{ *appendIO }

func (f appendNonregularFiles) OpenFile(path string, flag int, mode os.FileMode) (TranscriptFile, error) {
	file, e := f.appendIO.OpenFile(path, flag, mode)
	if e != nil {
		return nil, e
	}
	return appendNonregularFile{file}, nil
}

type appendNonregularFile struct{ TranscriptFile }

func (f appendNonregularFile) Stat() (os.FileInfo, error) {
	info, e := f.TranscriptFile.Stat()
	if e != nil {
		return info, e
	}
	return appendNonregularInfo{info}, nil
}

type appendNonregularInfo struct{ os.FileInfo }

func (info appendNonregularInfo) Mode() os.FileMode { return os.ModeDir }
func TestAppendNonregularCompletionStillAwaitsRealClose(t *testing.T) {
	owner := cooperative.New()
	io := &appendIO{armed: true, counts: map[string]int{}}
	w := actualPendingWriter(t, owner, t.TempDir(), appendNonregularFiles{io})
	if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(w.target(), nil, 0600); e != nil {
		t.Fatal(e)
	}
	var p *PendingWriter
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		var e error
		p, e = NewPendingWriter(w, owner)
		w.AdoptSessionFile()
		return e
	})
	t.Cleanup(func() { _ = p.Close(context.Background()); owner.Close() })
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("summary")})
	future := beginAppend(t, owner, p, appendBoundaryLoader(t, io), &entry)
	if e := future.Wait(context.Background()); e == nil || !strings.Contains(e.Error(), "Refusing non-regular transcript target") {
		t.Fatal(e)
	}
	io.mu.Lock()
	closed := io.counts["close"]
	io.mu.Unlock()
	if closed != 1 {
		t.Fatal("nonregular external stat completion did not close native descriptor", closed)
	}
}
func TestAppendOwnerStopDuringNativeStatClosesAdoptedLease(t *testing.T) {
	owner := cooperative.New()
	io := &appendIO{armed: true, hold: "stat", entered: make(chan struct{}), released: make(chan struct{}), counts: map[string]int{}}
	w := actualPendingWriter(t, owner, t.TempDir(), io)
	if e := os.MkdirAll(filepath.Dir(w.target()), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(w.target(), nil, 0600); e != nil {
		t.Fatal(e)
	}
	var p *PendingWriter
	pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
		var e error
		p, e = NewPendingWriter(w, owner)
		w.AdoptSessionFile()
		return e
	})
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("summary")})
	future := beginAppend(t, owner, p, appendBoundaryLoader(t, io), &entry)
	awaitSignal(t, io.entered, "actual stat completed")
	owner.Close()
	close(io.released)
	if e := future.Wait(context.Background()); !errors.Is(e, cooperative.ErrClosed) {
		t.Fatal(e)
	}
	p.workers.Wait()
	io.mu.Lock()
	closed := io.counts["close"]
	io.mu.Unlock()
	if closed != 1 {
		t.Fatal("native adopted stat lease leaked", closed)
	}
}
