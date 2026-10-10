package resumestate

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

// This file is a frozen, explicit offline capture of the actual Bun Source.
// Ordinary tests never regenerate backend testdata.
func TestActualSourceResumeStateContract(t *testing.T) {
	data, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Contracts []json.RawMessage `json:"contracts"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if len(fixture.Contracts) < 48 {
		t.Fatal("actual Source contract corpus missing")
	}
	for _, original := range fixture.Contracts {
		var name struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(original, &name); err != nil {
			t.Fatal(err)
		}
		t.Run(name.Name, func(t *testing.T) {
			var prior struct {
				Input struct {
					Home string `json:"home"`
				} `json:"input"`
			}
			if err := json.Unmarshal(original, &prior); err != nil {
				t.Fatal(err)
			}
			home := t.TempDir()
			raw := strings.ReplaceAll(string(original), prior.Input.Home, home)
			raw = strings.ReplaceAll(raw, sanitizePath(prior.Input.Home), sanitizePath(home))
			var row struct {
				Name  string `json:"name"`
				Input struct {
					Home, CurrentSessionID, EnvironmentKind, PolicyPath, JSONLPath string
					Bare, Checkpointing, HoldCopies                                bool
					Source                                                         *string
					UUIDProbes                                                     []string
					LiteReadFault                                                  *struct {
						Path   string
						AtRead int
					}
				}
				Files       map[string]string
				Times       map[string]int64
				Directories []string
				Expected    json.RawMessage
			}
			if err := json.Unmarshal([]byte(raw), &row); err != nil {
				t.Fatal(err)
			}
			for _, directory := range row.Directories {
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
			}
			for path, value := range row.Files {
				writePrivate(t, path, value)
				if milliseconds, exists := row.Times[path]; exists {
					timestamp := time.UnixMilli(milliseconds)
					if err := os.Chtimes(path, timestamp, timestamp); err != nil {
						t.Fatal(err)
					}
				}
			}
			held := make(chan struct{})
			files := &heldCopies{release: held}
			if row.Input.HoldCopies {
				files.started = make(chan struct{})
			}
			traced := &cacheProbeFiles{heldCopies: files}
			if row.Input.LiteReadFault != nil {
				traced.faultPath = row.Input.LiteReadFault.Path
				traced.atRead = row.Input.LiteReadFault.AtRead
			}
			options, events := testOptions(t, home, traced)
			options.CurrentSessionID = row.Input.CurrentSessionID
			options.Environment["CLAUDE_CODE_SIMPLE"] = fmt.Sprint(row.Input.Bare)
			options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = fmt.Sprint(row.Input.Checkpointing)
			options.EnvironmentKind = row.Input.EnvironmentKind
			options.PolicyPaths = []string{row.Input.PolicyPath}
			service, err := New(options)
			if err != nil {
				t.Fatal(err)
			}
			var source json.RawMessage
			if row.Input.Source != nil {
				source = json.RawMessage(*row.Input.Source)
			}
			result, loadError := service.Load(Input{Source: source, JSONLPath: row.Input.JSONLPath})
			var failureValue json.RawMessage
			var resultSource any
			if loadError != nil {
				failureValue = errorRow(loadError)
				events.phases = append(events.phases, "throw")
			} else {
				value, err := protocol.ParseAndStringifyJavaScriptJSON(encode(result))
				if err != nil {
					t.Fatal(err)
				}
				resultSource = string(value)
				events.phases = append(events.phases, "return")
			}
			state := func() any {
				value := service.State()
				return map[string]any{"skills": value.Skills, "planSlugs": value.PlanSlugs, "suppressed": value.SuppressNextSkillListing, "pendingEntries": value.PendingEntries}
			}
			before := state()
			close(held)
			service.AwaitBackground()
			after := state()
			effects := map[string]string{}
			for _, directory := range []string{filepath.Join(home, ".claude", "plans"), filepath.Join(home, "private-plans"), filepath.Join(home, ".claude", "file-history", row.Input.CurrentSessionID)} {
				err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
					if errors.Is(err, fs.ErrNotExist) {
						return nil
					}
					if err != nil {
						return err
					}
					if entry.Type().IsRegular() {
						data, err := os.ReadFile(path)
						if err != nil {
							return err
						}
						part, err := filepath.Rel(home, path)
						if err != nil {
							return err
						}
						effects[part] = hex.EncodeToString(data)
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			observation := map[string]any{"resultSource": resultSource, "before": before, "after": after, "phases": events.phases, "effects": effects, "allocations": events.allocations, "diagnostics": events.diagnostics, "errors": events.errors, "failure": failureValue}
			if len(row.Input.UUIDProbes) > 0 {
				var sid string
				if err := json.Unmarshal(source, &sid); err != nil {
					t.Fatal(err)
				}
				traced.probing.Store(true)
				probes := []map[string]any{}
				for _, uuid := range row.Input.UUIDProbes {
					exists := false
					for _, value := range service.State().SessionUUIDs[sid] {
						if scalarEqual(value, quote(uuid)) {
							exists = true
						}
					}
					probes = append(probes, map[string]any{"uuid": uuid, "exists": exists})
				}
				traced.probing.Store(false)
				observation["uuidProbes"] = probes
				observation["transcriptReadsDuringProbes"] = traced.reads.Load()
			}
			actual := encode(observation)
			actual, err = protocol.ParseAndStringifyJavaScriptJSON(actual)
			if err != nil {
				t.Fatal(err)
			}
			var expected, observed any
			if err := json.Unmarshal(row.Expected, &expected); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(actual, &observed); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expected, observed) {
				t.Fatalf("Source state/raw result differs\nexpected: %s\nactual: %s", row.Expected, actual)
			}
		})
	}
}

type cacheProbeFiles struct {
	*heldCopies
	probing   atomic.Bool
	reads     atomic.Int64
	faultPath string
	atRead    int
}

func (f *cacheProbeFiles) Open(path string) (sessionstore.File, error) {
	value, err := f.heldCopies.Open(path)
	if err == nil && path == f.faultPath {
		return &faultRead{File: value, fail: f.atRead}, nil
	}
	return value, err
}

func (f *cacheProbeFiles) ReadFile(path string) ([]byte, error) {
	if f.probing.Load() {
		f.reads.Add(1)
	}
	return f.heldCopies.ReadFile(path)
}
