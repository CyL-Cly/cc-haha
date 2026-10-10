package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// Physical read completion is gated once AFTER the real bytes were read. A new
// cache promise remains free to read the replacement while the old job waits.
type mutableIO struct {
	mu             sync.Mutex
	held           bool
	enter, release chan struct{}
}

func (f *mutableIO) Stat(path string) (int64, error) {
	v, e := os.Stat(path)
	if e != nil {
		return 0, e
	}
	return v.Size(), nil
}
func (f *mutableIO) ReadFile(path string) ([]byte, error) {
	b, e := os.ReadFile(path)
	if e != nil {
		return b, e
	}
	f.mu.Lock()
	hold := f.enter != nil && !f.held
	if hold {
		f.held = true
		close(f.enter)
	}
	f.mu.Unlock()
	if hold {
		<-f.release
	}
	return b, nil
}
func (f *mutableIO) Open(path string) (sessionstore.File, error) {
	return (sessionstore.OSFileSystem{}).Open(path)
}

type mutableTag struct {
	Missing bool            `json:"missing"`
	Raw     json.RawMessage `json:"raw"`
}

func (v mutableTag) value() json.RawMessage {
	if v.Missing {
		return nil
	}
	var s string
	_ = json.Unmarshal(v.Raw, &s)
	return json.RawMessage(s)
}

type mutableStep struct {
	Op, Slot     string
	Value, Probe mutableTag
	Bytes        string
}
type mutableProfile struct {
	Name  string
	Hold  bool
	Steps []mutableStep
}
type mutableCase struct {
	Name     string
	Original struct {
		Profile      mutableProfile
		SID, Content string
	}
	Expected struct {
		Rows       []json.RawMessage
		FinalBytes string
	}
}
type mutableSlots struct {
	future *SessionUUIDFuture
	set    *sessionhistory.MessageSet
	probe  json.RawMessage
}

type setObserver struct {
	sets     map[*sessionhistory.MessageSet]int
	promises map[*SessionUUIDFuture]int
}

func (o *setObserver) promise(f *SessionUUIDFuture) int {
	if o.promises[f] == 0 {
		o.promises[f] = len(o.promises) + 1
	}
	return o.promises[f]
}
func (o *setObserver) snapshot(s *cooperative.Scope, set *sessionhistory.MessageSet) (map[string]any, error) {
	if o.sets[set] == 0 {
		o.sets[set] = len(o.sets) + 1
	}
	values, e := set.OwnerKeys(s)
	if e != nil {
		return nil, e
	}
	keys := []any{}
	for _, key := range values {
		raw := key.Raw()
		item := map[string]any{}
		if raw == nil {
			item["type"] = "undefined"
		} else {
			encoded, err := protocol.ParseAndStringifyJavaScriptJSON(raw)
			if err != nil {
				return nil, err
			}
			item["raw"] = string(encoded)
			switch bytes.TrimSpace(raw)[0] {
			case '"':
				item["type"] = "string"
			case 't', 'f':
				item["type"] = "boolean"
			case 'n', '{', '[':
				item["type"] = "object"
			default:
				item["type"] = "number"
				if string(encoded) == "null" {
					if bytes.TrimSpace(raw)[0] == '-' {
						item["number"] = "-Infinity"
					} else {
						item["number"] = "Infinity"
					}
				}
			}
		}
		keys = append(keys, item)
	}
	return map[string]any{"set": o.sets[set], "keys": keys}, nil
}
func (o *setObserver) cache(s *cooperative.Scope, p *PendingWriter, sid string) (map[string]any, error) {
	f, found, e := p.LookupSessionUUIDCache(s, sid)
	if e != nil {
		return nil, e
	}
	if !found {
		return map[string]any{"present": false}, nil
	}
	v := map[string]any{"present": true, "promise": o.promise(f), "settled": false}
	select {
	case <-f.Done():
		set, e := f.Wait(context.Background())
		if e != nil {
			return nil, e
		}
		v["settled"] = true
		v["snapshot"], e = o.snapshot(s, set)
		return v, e
	default:
		return v, nil
	}
}
func TestMutableSetActualSourceContracts(t *testing.T) {
	b, e := os.ReadFile("testdata/mutable_set_source_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var contract struct{ Cases []mutableCase }
	if e = json.Unmarshal(b, &contract); e != nil {
		t.Fatal(e)
	}
	if len(contract.Cases) != 11 {
		t.Fatal("expected eleven original cache/Set inputs")
	}
	for _, c := range contract.Cases {
		t.Run(c.Name, func(t *testing.T) {
			owner, writer, pending, _ := uuidHarness(t, c.Original.Content, nil, false, c.Original.SID)
			fs := &mutableIO{}
			if c.Original.Profile.Hold {
				fs.enter = make(chan struct{})
				fs.release = make(chan struct{})
			}
			loader, e := sessionhistory.New(sessionhistory.Options{Files: fs, Stat: fs.Stat, ReadFile: fs.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
			if e != nil {
				t.Fatal(e)
			}
			o := &setObserver{sets: map[*sessionhistory.MessageSet]int{}, promises: map[*SessionUUIDFuture]int{}}
			slots := map[string]*mutableSlots{}
			rows := []any{}
			for _, step := range c.Original.Profile.Steps {
				row := map[string]any{"op": step.Op}
				switch step.Op {
				case "load", "begin-load":
					f := uuidBegin(t, owner, pending, loader, c.Original.SID)
					slot := &mutableSlots{future: f, probe: step.Probe.value()}
					slots[step.Slot] = slot
					row["promise"] = o.promise(f)
					if step.Op == "load" {
						slot.set, e = f.Wait(context.Background())
						if e != nil {
							t.Fatal(e)
						}
						pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
							row["found"], e = slot.set.OwnerHas(s, slot.probe)
							if e != nil {
								return e
							}
							row["snapshot"], e = o.snapshot(s, slot.set)
							return e
						})
					}
				case "held":
					awaitSignal(t, fs.enter, "real first completion")
					row["held"] = true
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["cache"], e = o.cache(s, pending, c.Original.SID); return e })
				case "release":
					close(fs.release)
					row["held"] = true
				case "await-load":
					slot := slots[step.Slot]
					slot.set, e = slot.future.Wait(context.Background())
					if e != nil {
						t.Fatal(e)
					}
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
						row["found"], e = slot.set.OwnerHas(s, slot.probe)
						if e != nil {
							return e
						}
						row["snapshot"], e = o.snapshot(s, slot.set)
						return e
					})
				case "cache":
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["cache"], e = o.cache(s, pending, c.Original.SID); return e })
				case "rewrite":
					if e = os.WriteFile(writer.target(), []byte(step.Bytes), 0600); e != nil {
						t.Fatal(e)
					}
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error { row["cache"], e = o.cache(s, pending, c.Original.SID); return e })
				case "clear-cache":
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
						if e = pending.ClearSessionUUIDCache(s); e != nil {
							return e
						}
						row["cache"], e = o.cache(s, pending, c.Original.SID)
						return e
					})
				case "prime":
					// Original getLastSessionLog performs a new real load before cache.has.
					// Selection itself is outside this component's declared differential domain.
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
						transcript, err := loader.LoadTranscriptFile(writer.target(), false)
						if err != nil {
							return err
						}
						_, _, e = pending.PrimeSessionUUIDCache(s, loader, c.Original.SID, transcript)
						if e != nil {
							return e
						}
						row["cache"], e = o.cache(s, pending, c.Original.SID)
						return e
					})
				case "snapshot", "add", "delete", "has", "clear-set":
					pendingCall(t, owner, cooperative.Read, func(s *cooperative.Scope) error {
						set := slots[step.Slot].set
						switch step.Op {
						case "add":
							e = set.Add(s, step.Value.value())
							row["same"] = e == nil
						case "delete":
							row["deleted"], e = set.Delete(s, step.Value.value())
						case "has":
							row["found"], e = set.OwnerHas(s, step.Value.value())
						case "clear-set":
							e = set.Clear(s)
						}
						if e != nil {
							return e
						}
						if step.Op != "has" {
							row["snapshot"], e = o.snapshot(s, set)
						}
						return e
					})
				default:
					t.Fatal("unsupported original program", step.Op)
				}
				rows = append(rows, row)
			}
			actual, e := json.Marshal(rows)
			if e != nil {
				t.Fatal(e)
			}
			expected, e := json.Marshal(c.Expected.Rows)
			if e != nil {
				t.Fatal(e)
			}
			var a, b any
			_ = json.Unmarshal(actual, &a)
			_ = json.Unmarshal(expected, &b)
			aa, _ := json.Marshal(a)
			bb, _ := json.Marshal(b)
			if !bytes.Equal(aa, bb) {
				t.Fatalf("Source cache/Set mismatch\nactual %s\nsource %s", aa, bb)
			}
			final, e := os.ReadFile(writer.target())
			if e != nil || string(final) != c.Expected.FinalBytes {
				t.Fatalf("complete original JSONL changed %q/%v", final, e)
			}
		})
	}
}
