package application

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualNativeDefaultCatalogKeepsWholeSourcePoolAndNoImplicitDenyRules(t *testing.T) {
	var captured []byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "actual-default", "", nil, "ACTUAL_DEFAULT_NATIVE_OK")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Arguments.Tools = nil
	o.Environment["ENABLE_TOOL_SEARCH"] = "false"
	o.Native.Environment["ENABLE_TOOL_SEARCH"] = "false"
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal("actual complete binding factory cannot open ordinary default", err)
	}
	defer s.Close(context.Background())
	if err = s.process.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		permission, err := s.process.Host.NativePermissionContext(scope)
		if err != nil {
			return err
		}
		var denies map[string][]string
		if err = json.Unmarshal(permission.Get("alwaysDenyRules"), &denies); err != nil {
			return err
		}
		if len(denies["cliArg"]) != 0 {
			t.Fatal("omitted default silently added explicit subset deny rules", denies)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = s.Submit(context.Background(), tui.Input{Text: "Use the ordinary default native Agent."}, nil); err != nil {
		t.Fatal(err)
	}
	var body struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	if err = json.Unmarshal(captured, &body); err != nil {
		t.Fatal(err)
	}
	names := []string{}
	for _, tool := range body.Tools {
		names = append(names, tool.Name)
	}
	want := []string{"Agent", "AskUserQuestion", "Bash", "Edit", "EnterPlanMode", "EnterWorktree", "ExitPlanMode", "ExitWorktree", "Glob", "Grep", "NotebookEdit", "Read", "Skill", "TaskOutput", "TaskStop", "TodoWrite", "WebFetch", "Workflow", "Write"}
	if !reflect.DeepEqual(names, want) {
		t.Fatal("ordinary omitted default was silently reduced or expanded", names, want)
	}
}
