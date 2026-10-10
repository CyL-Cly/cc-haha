package sessionlocator

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func nativeOptions(t *testing.T) (Options, string) {
	t.Helper()
	home := t.TempDir()
	root := filepath.Join(home, "projects")
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal(err)
	}
	budget := sessionstore.NewBudget()
	options := Options{ConfigDir: func() string { return home }, Reader: sessionstore.NewReaderWithBudget(nil, budget), Budget: budget, Compare: sessionhistory.CompareStrings, ReadDir: func(path string) ([]string, error) {
		items, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		result := []string{}
		for _, item := range items {
			result = append(result, item.Name())
		}
		return result, nil
	}, Stat: func(path string) (float64, error) {
		item, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return float64(item.ModTime().UnixNano()) / 1e6, nil
	}}
	return options, root
}

const fixtureID = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"

func putCandidate(t *testing.T, root, directory, content string, stamp int64) string {
	t.Helper()
	folder := filepath.Join(root, directory)
	if err := os.Mkdir(folder, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(folder, fixtureID+".jsonl")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	when := time.Unix(stamp, 0)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestRealFilesystemSelectionPriorityAndFreshDiscovery(t *testing.T) {
	options, root := nativeOptions(t)
	real := `{"type":"user","message":{"role":"user","content":"question"}}` + "\n"
	putCandidate(t, root, "-a", real, 1700000000)
	putCandidate(t, root, "-B", real, 1700000000)
	newer := putCandidate(t, root, "-z", real, 1700000010)
	putCandidate(t, root, "-placeholder", `{"type":"session-meta"}`+"\n", 1700000020)
	putCandidate(t, root, "-hidden", `{"type":"user","isMeta":true,"message":{"role":"user","content":"internal"}}`+"\n", 1700000030)
	if err := os.Mkdir(filepath.Join(root, "-missing"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "ordinary-project-entry"), nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "-directory"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(root, "-directory", fixtureID+".jsonl"), 0700); err != nil {
		t.Fatal(err)
	}
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	values, err := service.FindFiles(fixtureID)
	if err != nil || len(values) != 5 || values[0].FilePath != newer || values[1].ProjectDir != "-a" || values[2].ProjectDir != "-B" || values[3].ProjectDir != "-hidden" {
		t.Fatal(values, err)
	}
	var pending sync.WaitGroup
	failures := make(chan error, 8)
	for range 8 {
		pending.Add(1)
		go func() {
			defer pending.Done()
			value, err := service.FindFile(fixtureID)
			if err != nil {
				failures <- err
			} else if value == nil || value.FilePath != newer {
				failures <- errors.New("concurrent selection changed")
			}
		}()
	}
	pending.Wait()
	close(failures)
	for err := range failures {
		t.Fatal(err)
	}
	if err := os.Remove(newer); err != nil {
		t.Fatal(err)
	}
	value, err := service.FindFile(fixtureID)
	if err != nil || value == nil || value.ProjectDir != "-a" {
		t.Fatal("stale discovery after deletion", value, err)
	}
	for _, id := range []string{"invalid", strings.Repeat("f", 36), "ffffffff-ffff-ffff-ffff-ffffffffffff"} {
		if value, err := service.FindFile(id); err != nil || value != nil {
			t.Fatal(value, err)
		}
	}
}
func TestOversizedRecordAndSourceSkippedDiscoveryFaults(t *testing.T) {
	options, root := nativeOptions(t)
	putCandidate(t, root, "-oversized", `{"type":"future","content":"`+strings.Repeat("x", 9*1024*1024)+`"}`+"\n", 1700000000)
	service, err := New(options)
	if err != nil {
		t.Fatal(err)
	}
	value, err := service.FindFile(fixtureID)
	if err != nil || value == nil || value.ProjectDir != "-oversized" {
		t.Fatal(value, err)
	}
	failed := errors.New("private discovery fixture error")
	options.ReadDir = func(string) ([]string, error) { return nil, failed }
	service, _ = New(options)
	if value, err := service.FindFile(fixtureID); err != nil || value != nil {
		t.Fatal("Source readdir catch changed", value, err)
	}
	options, _ = nativeOptions(t)
	options.Stat = func(string) (float64, error) { return 0, failed }
	service, _ = New(options)
	if value, err := service.FindFile(fixtureID); err != nil || value != nil {
		t.Fatal(value, err)
	}
}
func TestRequiredSharedBudgetAndCollationFailure(t *testing.T) {
	options, root := nativeOptions(t)
	for _, directory := range []string{"-a", "-b", "-c"} {
		putCandidate(t, root, directory, "", 1700000000)
	}
	for _, remove := range []func(*Options){func(o *Options) { o.ConfigDir = nil }, func(o *Options) { o.ReadDir = nil }, func(o *Options) { o.Stat = nil }, func(o *Options) { o.Reader = nil }, func(o *Options) { o.Budget = nil }, func(o *Options) { o.Compare = nil }} {
		copy := options
		remove(&copy)
		if _, err := New(copy); err == nil {
			t.Fatal("missing production dependency accepted")
		}
	}
	failure := errors.New("unsupported host collation")
	options.Compare = func(json.RawMessage, json.RawMessage) (int, error) { return 0, failure }
	service, _ := New(options)
	if _, err := service.FindFiles(fixtureID); !errors.Is(err, failure) {
		t.Fatal(err)
	}
}
func TestActualCollaborationAndConversationRejectedProfiles(t *testing.T) {
	for _, value := range []string{`[]`, `{}`, `{"senderSessionId":"s","messageId":"d","text":""}`, `{"senderSessionId":1,"messageId":"d","text":"task"}`, `invalid`} {
		raw, _ := json.Marshal(collaborationPrefix + value)
		if collaboration(raw) {
			t.Fatal(value)
		}
	}
	for _, raw := range []string{`{"type":"future"}`, `{"type":"user","message":{"role":""}}`, `{"type":"user","isMeta":true,"message":{"role":"user","content":[]}}`, `{"type":"assistant","isMeta":true,"message":{"role":"assistant","content":"task"}}`} {
		if conversation(json.RawMessage(raw)) {
			t.Fatal(raw)
		}
	}
	if !conversation(json.RawMessage(`{"type":"system","message":{"role":"system"}}`)) {
		t.Fatal("Source system conversation ignored")
	}
	if !truthy(json.RawMessage(`1e400`)) || !truthy(json.RawMessage(`"x"`)) || !truthy(json.RawMessage(`{}`)) {
		t.Fatal("Source truthy primitives changed")
	}
}
func TestActualCollaborationShapeAndFalsyFields(t *testing.T) {
	content, _ := json.Marshal(collaborationPrefix + `{"senderSessionId":"sender","messageId":"delivery","text":"task"}`)
	raw := json.RawMessage(`{"type":"user","isMeta":true,"message":{"role":"user","content":` + string(content) + `}}`)
	if !collaboration(content) || !conversation(raw) {
		t.Fatal("valid Source collaboration delivery rejected", string(content), string(raw))
	}
	for _, raw := range []string{`null`, `true`, `"wrong prefix"`, `0e400`} {
		if collaboration(json.RawMessage(raw)) {
			t.Fatal(raw)
		}
	}
	for _, value := range []string{`null`, `false`, `0e400`, `-0.0`, `""`} {
		if truthy(json.RawMessage(value)) {
			t.Fatal("falsy field became transcript", value)
		}
	}
}
