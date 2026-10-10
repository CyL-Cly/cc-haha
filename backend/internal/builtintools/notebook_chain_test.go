package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const nativeNotebookFile = `{"cells":[{"cell_type":"code","id":"original","source":["print(1)\n"],"execution_count":2,"outputs":[{"output_type":"stream","text":"old output"}],"metadata":{"unknown":"retained"}}],"metadata":{"language_info":{"name":"python"}},"nbformat":4,"nbformat_minor":5,"unknown":["retained"]}`

func TestActualNotebookReadEditReadInsertDeleteUseSharedLiveCache(t *testing.T) {
	s, owner, home, _ := fixture(t)
	s.options.NonInteractive = true
	s.options.Random = func() float64 { return .5 }
	p := filepath.Join(home, "actual.ipynb")
	if err := os.WriteFile(p, []byte(nativeNotebookFile), 0640); err != nil {
		t.Fatal(err)
	}
	read := call(t, s, owner, "Read", input(`{"file_path":"actual.ipynb"}`))
	if input(string(read)).String("type") != "notebook" {
		t.Fatal("Read did not process notebook cells")
	}
	dedup := call(t, s, owner, "Read", input(`{"file_path":"actual.ipynb"}`))
	if input(string(dedup)).String("type") != "file_unchanged" {
		t.Fatal("unchanged Read no longer deduplicates")
	}
	updated := call(t, s, owner, "NotebookEdit", input(`{"notebook_path":"actual.ipynb","cell_id":"original","new_source":"print(2)\ud800"}`))
	if input(string(updated)).String("cell_id") != "original" {
		t.Fatal("replace cell identity changed")
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		state, ok, err := s.options.Cache.Get(scope, p)
		if err != nil {
			return err
		}
		stamp, err := mtime(p)
		if err != nil {
			return err
		}
		if !ok || state.Offset != nil || state.Limit != nil || state.Timestamp != stamp || !jsonSame(state.Content, input(string(updated)).Get("updated_file")) {
			return fmt.Errorf("edit cache lost real content/mtime or retained read dedup range")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	read = call(t, s, owner, "Read", input(`{"file_path":"actual.ipynb"}`))
	if input(string(read)).String("type") != "notebook" {
		t.Fatal("Read after NotebookEdit returned stale unchanged stub")
	}
	mapped, err := MapResult("Read", read, quote("real-read"))
	if err != nil || !strings.Contains(string(raw(mapped)), `print(2)\ud800`) || strings.Contains(string(raw(mapped)), "old output") {
		t.Fatalf("live post-edit result %s %v", raw(mapped), err)
	}
	inserted := call(t, s, owner, "NotebookEdit", input(`{"notebook_path":"actual.ipynb","cell_id":"original","new_source":"# inserted","cell_type":"markdown","edit_mode":"insert"}`))
	if input(string(inserted)).String("cell_id") != "i" {
		t.Fatal("actual random producer not used for insertion")
	}
	_ = call(t, s, owner, "NotebookEdit", input(`{"notebook_path":"actual.ipynb","cell_id":"i","new_source":"","edit_mode":"delete"}`))
	bytes, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cells []protocol.JSONObject
	file := input(string(bytes))
	if err := json.Unmarshal(file.Get("cells"), &cells); err != nil {
		t.Fatal(err)
	}
	if len(cells) != 1 || cells[0].String("id") != "original" || !jsonSame(cells[0].Get("source"), []byte(`"print(2)\ud800"`)) || string(cells[0].Get("outputs")) != "[]" || !jsonSame(file.Get("unknown"), []byte(`["retained"]`)) {
		t.Fatalf("actual notebook mutation lost cell state/unknown fields %s", bytes)
	}
	future := time.Now().Add(time.Second)
	if err := os.Chtimes(p, future, future); err != nil {
		t.Fatal(err)
	}
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		err := s.Validate(context.Background(), "NotebookEdit", input(`{"notebook_path":"actual.ipynb","cell_id":"original","new_source":"stale"}`), scope)
		var validation *builtinwrite.ValidationError
		if !errors.As(err, &validation) || validation.ErrorCode != 10 {
			return fmt.Errorf("external edit did not trigger stale read: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookPermissionUsesOriginalEditRulesAndCatalogDeny(t *testing.T) {
	s, owner, home, permissions := fixture(t)
	in := input(`{"notebook_path":"private.ipynb","cell_id":"cell-0","new_source":"new"}`)
	for _, row := range []struct{ mode, rule, key, want string }{
		{"default", "", "", "ask"},
		{"acceptEdits", "", "", "allow"},
		{"default", "Edit(private.ipynb)", "alwaysAllowRules", "allow"},
		{"acceptEdits", "Edit(private.ipynb)", "alwaysAskRules", "ask"},
		{"bypassPermissions", "Edit(private.ipynb)", "alwaysDenyRules", "deny"},
	} {
		permissions.Set("mode", quote(row.mode))
		for _, key := range []string{"alwaysAllowRules", "alwaysDenyRules", "alwaysAskRules"} {
			permissions.Set(key, raw(map[string][]string{}))
		}
		if row.key != "" {
			permissions.Set(row.key, raw(map[string][]string{"projectSettings": {row.rule}}))
		}
		if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			decision, err := s.PermissionDecision(scope, "NotebookEdit", &in)
			if err != nil {
				return err
			}
			if decision.Behavior != row.want {
				return fmt.Errorf("permission %s/%s: %s != %s", row.mode, row.rule, decision.Behavior, row.want)
			}
			if row.want == "deny" && !jsonSame(decision.Message, quote("Permission to edit "+filepath.Join(home, "private.ipynb")+" has been denied.")) {
				return fmt.Errorf("Notebook permission denial path/message changed: %s", decision.Message)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	permissions.Set("alwaysDenyRules", raw(map[string][]string{"cliArg": {"NotebookEdit"}}))
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		defs, tools, err := s.Catalog(scope)
		if err != nil {
			return err
		}
		for _, definition := range defs {
			if definition.Name == "NotebookEdit" {
				return fmt.Errorf("whole-tool deny exposed model schema")
			}
		}
		for _, tool := range tools {
			if tool.Name == "NotebookEdit" {
				return fmt.Errorf("whole-tool deny exposed executable registration")
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookEnabledHistoryRequiresActualProducerAndHonorsDisable(t *testing.T) {
	for _, row := range []struct {
		interactive       bool
		enabled, disabled string
		wantCap           bool
	}{
		{true, "", "", true}, {false, "true", "", true}, {true, "true", "yes", false}, {false, "ON", "true", false}, {false, "false", "", false},
	} {
		s, owner, home, _ := fixture(t)
		s.options.NonInteractive = !row.interactive
		s.options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = row.enabled
		s.options.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"] = row.disabled
		p := filepath.Join(home, "history.ipynb")
		if err := os.WriteFile(p, []byte(nativeNotebookFile), 0640); err != nil {
			t.Fatal(err)
		}
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			_, err := s.Call(context.Background(), "NotebookEdit", input(`{"notebook_path":"history.ipynb","cell_id":"original","new_source":"new"}`), scope, false)
			return err
		})
		if row.wantCap {
			var capError *CapabilityError
			if !errors.As(err, &capError) || !strings.Contains(capError.Boundary, "actual fileHistoryTrackEdit") {
				t.Fatalf("enabled history silently ignored: %v", err)
			}
			bytes, readErr := os.ReadFile(p)
			if readErr != nil || string(bytes) != nativeNotebookFile {
				t.Fatalf("cap mutated notebook: %s %v", bytes, readErr)
			}
		} else if err != nil {
			t.Fatalf("Source disabled file history rejected: %v", err)
		}
	}
}

func TestNotebookAbsolutePathKeepsSourceCacheIdentity(t *testing.T) {
	s, owner, home, _ := fixture(t)
	s.options.NonInteractive = true
	p := filepath.Join(home, "identity.ipynb")
	if err := os.WriteFile(p, []byte(nativeNotebookFile), 0640); err != nil {
		t.Fatal(err)
	}
	_ = call(t, s, owner, "Read", input(`{"file_path":"identity.ipynb"}`))
	rawPath := home + "/./identity.ipynb"
	in := protocol.NewJSONObject(field("notebook_path", rawPath), field("cell_id", "original"), field("new_source", "new"))
	if err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		path, err := s.notebookPath(in)
		if err != nil {
			return err
		}
		if path != rawPath {
			return fmt.Errorf("absolute notebook path was normalized before Source cache lookup")
		}
		if err = s.Validate(context.Background(), "NotebookEdit", in, scope); err != nil {
			return fmt.Errorf("Source normalized FileStateCache key failed prior Read: %v", err)
		}
		unread := in.Clone()
		unread.Set("notebook_path", quote(home+"/./unread.ipynb"))
		var failure *builtinwrite.ValidationError
		err = s.Validate(context.Background(), "NotebookEdit", unread, scope)
		if !errors.As(err, &failure) || failure.ErrorCode != 9 {
			return fmt.Errorf("actually unread file bypassed original Read guard: %v", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestNotebookOutputEnvironmentUsesSourcePositiveParseIntAndClamp(t *testing.T) {
	for _, row := range []struct {
		value string
		want  int
	}{{"  12suffix", 12}, {"1e3", 1}, {"0x10", 30000}, {"invalid", 30000}, {"-1", 30000}, {"150001", 150000}, {strings.Repeat("9", 309), 150000}} {
		if got := notebookIntEnvironment(row.value, 30000, 150000); got != row.want {
			t.Fatalf("Source bounded positive parseInt %q: %d != %d", row.value, got, row.want)
		}
	}
}
