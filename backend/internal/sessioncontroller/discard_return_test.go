package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Native stat remains OSFiles.Stat. Sys is consumed in discard's owner callback;
// the observer posts one competing microtask at that cutpoint. It cannot select
// or prepare messages, and never executes mutable business on the worker.
type discardReturnFiles struct {
	OSFiles
	target   string
	observer func()
	once     sync.Once
}

func (files *discardReturnFiles) Stat(path string) (fs.FileInfo, error) {
	info, err := files.OSFiles.Stat(path)
	if err != nil || path != files.target {
		return info, err
	}
	return discardReturnInfo{FileInfo: info, notify: func() { files.once.Do(files.observer) }}, nil
}

type discardReturnInfo struct {
	fs.FileInfo
	notify func()
}

func (info discardReturnInfo) Sys() any { info.notify(); return info.FileInfo.Sys() }

func TestDiscardAsyncReturnPreservesCompetingOwnerReaction(t *testing.T) {
	for _, mutate := range []bool{true, false} {
		name := "observer-only"
		if mutate {
			name = "late-original-uuid"
		}
		t.Run(name, func(t *testing.T) {
			files := &discardReturnFiles{}
			owner, writer, pending, loader := recordHost(t, files, nil)
			files.target = writer.target()
			if err := os.MkdirAll(filepath.Dir(files.target), 0700); err != nil {
				t.Fatal(err)
			}
			old := `{"type":"user","uuid":"existing","timestamp":"2023-11-14T22:13:20.000Z","parentUuid":null,"message":{"role":"user","content":"original old user"}}` + "\n"
			if err := os.WriteFile(files.target, []byte(old), 0600); err != nil {
				t.Fatal(err)
			}
			cached := uuidBegin(t, owner, pending, loader, writer.options.SessionID)
			if _, err := cached.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			message := plainMessage("existing", "original late message")
			observed := make(chan struct{})
			var observerError error
			files.observer = func() {
				_, observerError = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
					if mutate {
						message.Set("uuid", quote("late-original-uuid"))
					}
					close(observed)
					return nil
				})
			}
			future := startRecord(t, owner, pending, loader, []LiveRecord{{Record: message}}, RecordOptions{PromptID: func() json.RawMessage { return nil }})
			id, err := future.Wait(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			awaitSignal(t, observed, "actual competing owner reaction")
			if observerError != nil {
				t.Fatal(observerError)
			}
			if err = flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			expected := "existing"
			if mutate {
				expected = "late-original-uuid"
			}
			if text(id) != expected {
				t.Errorf("Record resumed before async discard returned: UUID %s, expected %q", id, expected)
			}
			bytes := recordBytes(t, writer)
			if mutate && !strings.Contains(bytes, `"uuid":"late-original-uuid"`) {
				t.Errorf("original outer mutation was lost: %s", bytes)
			}
			if !mutate && bytes != old {
				t.Errorf("observer-only appended a duplicate: %s", bytes)
			}
			pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
				if pending.tracked != 0 {
					return errors.New("tracked write leaked after actual flush")
				}
				set, err := cached.Await(scope)
				if err != nil {
					return err
				}
				found, err := set.OwnerHas(scope, quote("late-original-uuid"))
				if err != nil {
					return err
				}
				if found != mutate {
					return errors.New("cache Set does not reflect selected original UUID")
				}
				return nil
			})
		})
	}
}

func TestDiscardAsyncReturnActualSourceOriginalRecords(t *testing.T) {
	data, err := os.ReadFile("testdata/discard_return_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Cases []struct {
			Input struct {
				Profile           struct{ Name string }
				SessionID, Target string
				Messages          []json.RawMessage
				LateEdits         struct{ UUID string }
			}
			Files    map[string]string
			Observed struct {
				Result   string
				CacheNew bool
				Bytes    string
			}
			AfterMode uint32
		}
	}
	if err = json.Unmarshal(data, &contract); err != nil || len(contract.Cases) != 2 {
		t.Fatalf("actual Source contract: %v", err)
	}
	for _, row := range contract.Cases {
		t.Run(row.Input.Profile.Name, func(t *testing.T) {
			files := &discardReturnFiles{}
			owner, writer, pending, loader := recordHost(t, files, nil)
			home := writer.options.CWD
			replace := func(value string) string {
				return strings.ReplaceAll(strings.ReplaceAll(value, "<private-project-key>", sanitize(home)), "<private-home>", home)
			}
			pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error { writer.Switch(row.Input.SessionID, ""); writer.Reset(); return nil })
			files.target = writer.target()
			if files.target != replace(row.Input.Target) {
				t.Fatalf("original target: %s != %s", files.target, replace(row.Input.Target))
			}
			// Files and messages originate only in Source's recorded original input.
			// Source observation is used below solely as the assertion oracle.
			for path, bytes := range row.Files {
				target := replace(path)
				if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(target, []byte(replace(bytes)), 0644); err != nil {
					t.Fatal(err)
				}
			}
			originals := make([]LiveRecord, len(row.Input.Messages))
			for i, raw := range row.Input.Messages {
				message := new(protocol.JSONObject)
				if err := json.Unmarshal(raw, message); err != nil {
					t.Fatal(err)
				}
				originals[i] = LiveRecord{Record: message}
			}
			cached := uuidBegin(t, owner, pending, loader, row.Input.SessionID)
			if _, err := cached.Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			observed := make(chan struct{})
			var observerError error
			files.observer = func() {
				_, observerError = owner.Microtask(cooperative.Read, func(*cooperative.Scope) error {
					if row.Input.Profile.Name == "stat-return-mutate-uuid" {
						originals[1].Record.Set("uuid", quote(row.Input.LateEdits.UUID))
					}
					close(observed)
					return nil
				})
			}
			future := startRecord(t, owner, pending, loader, originals, RecordOptions{PromptID: func() json.RawMessage { return nil }})
			id, err := future.Wait(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			awaitSignal(t, observed, "Source-original stat reaction")
			if observerError != nil {
				t.Fatal(observerError)
			}
			if err = flushRecord(t, owner, pending).Wait(context.Background()); err != nil {
				t.Fatal(err)
			}
			if text(id) != row.Observed.Result {
				t.Errorf("result %s, actual Source %q", id, row.Observed.Result)
			}
			if bytes := recordBytes(t, writer); bytes != replace(row.Observed.Bytes) {
				t.Errorf("actual Source JSONL bytes differ\nGo: %s\nSource: %s", bytes, replace(row.Observed.Bytes))
			}
			info, err := os.Stat(files.target)
			if err != nil {
				t.Fatal(err)
			}
			if uint32(info.Mode().Perm()) != row.AfterMode {
				t.Errorf("mode %o, actual Source %o", info.Mode().Perm(), row.AfterMode)
			}
			pendingCall(t, owner, cooperative.Read, func(scope *cooperative.Scope) error {
				set, err := cached.Await(scope)
				if err != nil {
					return err
				}
				found, err := set.OwnerHas(scope, quote(row.Input.LateEdits.UUID))
				if err != nil {
					return err
				}
				if found != row.Observed.CacheNew {
					return errors.New("actual Source cache membership differs")
				}
				if pending.tracked != 0 {
					return errors.New("actual Record flush retained tracked obligation")
				}
				return nil
			})
		})
	}
}
