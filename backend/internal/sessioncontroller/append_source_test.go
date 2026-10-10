package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type appendFault struct{ op string }

func (e appendFault) Error() string { return "fixture append completion failure: " + e.op }
func (e appendFault) Code() string {
	if e.op == "close" {
		return "EBADF"
	}
	return "EIO"
}

type appendIO struct {
	OSFiles
	mu                sync.Mutex
	armed, held       bool
	hold, fault       string
	entered, released chan struct{}
	counts            map[string]int
}

func (f *appendIO) count(op string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.armed {
		f.counts[op]++
	}
}
func (f *appendIO) gate(op string) {
	f.mu.Lock()
	hold := f.armed && f.hold == op && !f.held
	if hold {
		f.held = true
		close(f.entered)
	}
	f.mu.Unlock()
	if hold {
		<-f.released
	}
}
func (f *appendIO) MkdirAll(p string, m fs.FileMode) error { return os.MkdirAll(p, m) }
func (f *appendIO) OpenFile(p string, flag int, m fs.FileMode) (TranscriptFile, error) {
	f.count("open")
	file, e := (OSFiles{}).OpenFile(p, flag, m)
	if e != nil {
		return file, e
	}
	f.gate("open")
	if f.armed && f.fault == "open" {
		_ = file.Close()
		return nil, appendFault{"open"}
	}
	return &appendFile{TranscriptFile: file, io: f}, nil
}
func (f *appendIO) StatSize(p string) (int64, error) {
	v, e := os.Stat(p)
	if e != nil {
		return 0, e
	}
	return v.Size(), nil
}
func (f *appendIO) ReadFile(p string) ([]byte, error) {
	b, e := os.ReadFile(p)
	if e == nil {
		f.count("readFile")
		f.gate("readFile")
	}
	return b, e
}
func (f *appendIO) Open(p string) (sessionstore.File, error) {
	return (sessionstore.OSFileSystem{}).Open(p)
}

type appendFile struct {
	TranscriptFile
	io *appendIO
}

func (f *appendFile) Stat() (fs.FileInfo, error) {
	f.io.count("stat")
	v, e := f.TranscriptFile.Stat()
	if e != nil {
		return v, e
	}
	f.io.gate("stat")
	if f.io.armed && (f.io.fault == "stat" || f.io.fault == "stat-close") {
		return v, appendFault{"stat"}
	}
	return v, nil
}
func (f *appendFile) Close() error {
	f.io.count("close")
	e := f.TranscriptFile.Close()
	f.io.gate("close")
	if f.io.armed && (f.io.fault == "close" || f.io.fault == "stat-close") {
		return appendFault{"close"}
	}
	return e
}
func (f *appendFile) Write(b []byte) (int, error) {
	f.io.count("append")
	return f.TranscriptFile.Write(b)
}

type appendAction struct {
	Op, Field string
	Value     json.RawMessage
}
type appendProfile struct {
	Name, Pointer, Hold, Fault string
	Disabled                   bool
	Entry                      protocol.JSONObject
	Actions                    []appendAction
}
type appendCase struct {
	Name     string
	Original struct {
		Profile appendProfile
		SID     string
		Content *string
	}
	Expected struct {
		Rows       []json.RawMessage
		FinalBytes *string
		Counts     map[string]int
	}
}

func appendSnapshot(s *cooperative.Scope, p *PendingWriter, sid string) (map[string]any, error) {
	w := p.writer
	w.mu.Lock()
	v := map[string]any{"pending": []string{}, "queued": []string{}, "tracked": p.tracked, "identity": false, "cache": false, "excluded": []any{}}
	for _, e := range w.pending {
		b, err := e.MarshalJSON()
		if err == nil {
			b, err = protocol.ParseAndStringifyJavaScriptJSON(b)
		}
		if err != nil {
			w.mu.Unlock()
			return nil, err
		}
		v["pending"] = append(v["pending"].([]string), string(b))
	}
	for _, e := range w.queues[w.target()] {
		b, err := e.entry.MarshalJSON()
		if err == nil {
			b, err = protocol.ParseAndStringifyJavaScriptJSON(b)
		}
		if err != nil {
			w.mu.Unlock()
			return nil, err
		}
		v["queued"] = append(v["queued"].([]string), string(b))
	}
	_, v["identity"] = w.identities[w.target()]
	for key := range w.excluded {
		v["excluded"] = append(v["excluded"].([]any), map[string]any{"type": "string", "raw": key})
	}
	w.mu.Unlock()
	f, present, e := p.LookupSessionUUIDCache(s, sid)
	if e != nil {
		return nil, e
	}
	v["cache"] = present
	if present {
		select {
		case <-f.Done():
			set, e := f.Wait(context.Background())
			if e != nil {
				return nil, e
			}
			keys, e := set.OwnerKeys(s)
			if e != nil {
				return nil, e
			}
			out := []any{}
			for _, key := range keys {
				raw := key.Raw()
				r := map[string]any{"type": "string", "raw": string(raw)}
				if raw == nil {
					r = map[string]any{"type": "undefined"}
				}
				out = append(out, r)
			}
			v["keys"] = out
		default:
		}
	}
	return v, nil
}
func beginAppend(t *testing.T, owner *cooperative.Owner, p *PendingWriter, loader *sessionhistory.Loader, entry *protocol.JSONObject) *WriteFuture {
	t.Helper()
	var f *WriteFuture
	pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { var e error; f, e = p.BeginAppend(s, loader, entry, ""); return e })
	return f
}
func TestActualSourceAppendEntryAndEnqueue(t *testing.T) {
	b, e := os.ReadFile("testdata/append_source_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct{ Cases []appendCase }
	if e = json.Unmarshal(b, &contract); e != nil {
		t.Fatal(e)
	}
	if len(contract.Cases) != 20 {
		t.Fatal("twenty actual original append profiles required")
	}
	for _, c := range contract.Cases {
		t.Run(c.Name, func(t *testing.T) {
			owner := cooperative.New()
			fs := &appendIO{hold: c.Original.Profile.Hold, fault: c.Original.Profile.Fault, counts: map[string]int{"open": 0, "stat": 0, "close": 0, "append": 0, "readFile": 0}}
			if fs.hold != "" {
				fs.entered = make(chan struct{})
				fs.released = make(chan struct{})
			}
			w := actualPendingWriter(t, owner, t.TempDir(), fs)
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { w.Switch(c.Original.SID, ""); return nil })
			target := w.target()
			if e = os.MkdirAll(filepath.Dir(target), 0700); e != nil {
				t.Fatal(e)
			}
			if c.Original.Content != nil {
				if e = os.WriteFile(target, []byte(*c.Original.Content), 0600); e != nil {
					t.Fatal(e)
				}
			}
			var p *PendingWriter
			pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { p, e = NewPendingWriter(w, owner); return e })
			t.Cleanup(func() { _ = p.Close(context.Background()); owner.Close() })
			loader, e := sessionhistory.New(sessionhistory.Options{Files: fs, Stat: fs.StatSize, ReadFile: fs.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
			if e != nil {
				t.Fatal(e)
			}
			if c.Original.Profile.Pointer == "known" {
				var prep *PreparationFuture
				pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { prep, e = p.Prepare(s, target); return e })
				if e = prep.Wait(context.Background()); e != nil {
					t.Fatal(e)
				}
			}
			if c.Original.Profile.Pointer != "unset" {
				pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { w.AdoptSessionFile(); return nil })
			}
			if c.Original.Profile.Disabled {
				pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { w.options.SessionPersistenceDisabled = true; return nil })
			}
			fs.mu.Lock()
			fs.armed = true
			fs.mu.Unlock()
			entry := c.Original.Profile.Entry
			var future *WriteFuture
			rows := []any{}
			if c.Original.Profile.Actions[0].Op == "immediate" {
				pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
					var err error
					future, err = p.BeginAppend(s, loader, &entry, "")
					if err != nil {
						return err
					}
					row := map[string]any{"op": "immediate", "settled": false}
					select {
					case <-future.Done():
						row["settled"] = true
					default:
					}
					row["state"], err = appendSnapshot(s, p, c.Original.SID)
					rows = append(rows, row)
					return err
				})
			} else {
				future = beginAppend(t, owner, p, loader, &entry)
			}
			for _, action := range c.Original.Profile.Actions {
				if action.Op == "immediate" {
					continue
				}
				row := map[string]any{"op": action.Op}
				switch action.Op {
				case "held":
					awaitSignal(t, fs.entered, "actual native "+fs.hold)
					row["held"] = true
					row["settled"] = false
					select {
					case <-future.Done():
						row["settled"] = true
					default:
					}
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["state"], e = appendSnapshot(s, p, c.Original.SID); return e })
				case "mutate":
					pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error { entry.Set(action.Field, action.Value); return nil })
					row["field"] = action.Field
					row["value"] = action.Value
				case "release":
					close(fs.released)
				case "await":
					err := future.Wait(context.Background())
					outcome := map[string]any{"resolved": err == nil}
					if err != nil {
						outcome["message"] = err.Error()
						var fault appendFault
						if errors.As(err, &fault) {
							outcome["code"] = fault.Code()
						}
					}
					row["outcome"] = outcome
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["state"], e = appendSnapshot(s, p, c.Original.SID); return e })
				case "flush":
					var f *FlushFuture
					pendingCall(t, owner, cooperative.Flush, func(s *cooperative.Scope) error { f, e = p.Flush(s); return e })
					if e = f.Wait(context.Background()); e != nil {
						t.Fatal(e)
					}
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["state"], e = appendSnapshot(s, p, c.Original.SID); return e })
				case "natural":
					if e = future.Wait(context.Background()); e != nil {
						t.Fatal(e)
					}
					time.Sleep(160 * time.Millisecond)
					var f *FlushFuture
					pendingCall(t, owner, cooperative.Flush, func(s *cooperative.Scope) error { f, e = p.Flush(s); return e })
					if e = f.Wait(context.Background()); e != nil {
						t.Fatal(e)
					}
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["state"], e = appendSnapshot(s, p, c.Original.SID); return e })
				default:
					t.Fatal(action.Op)
				}
				rows = append(rows, row)
			}
			actual, _ := json.Marshal(rows)
			expected, _ := json.Marshal(c.Expected.Rows)
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(expected, &b)
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if !bytes.Equal(aa, bb) {
				t.Fatalf("original append mismatch\nactual%s\nsource%s", aa, bb)
			}
			final, e := fs.ReadFile(target)
			if c.Expected.FinalBytes == nil {
				if !errors.Is(e, os.ErrNotExist) {
					t.Fatal("expected absent target", e)
				}
			} else if e != nil || string(final) != *c.Expected.FinalBytes {
				t.Fatalf("literal JSONL %q vs %q (%v)", final, *c.Expected.FinalBytes, e)
			}
			fs.mu.Lock()
			actualCounts, _ := json.Marshal(fs.counts)
			fs.mu.Unlock()
			sourceCounts, _ := json.Marshal(c.Expected.Counts)
			if !bytes.Equal(actualCounts, sourceCounts) {
				t.Fatalf("native IO completion counts %s vs %s", actualCounts, sourceCounts)
			}
		})
	}
}
