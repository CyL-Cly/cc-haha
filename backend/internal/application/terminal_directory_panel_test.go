package application

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestTerminalAddDirectorySessionRememberPermissionContextAndNextExecution(t *testing.T) {
	var requests atomic.Int32
	var directory, target string
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch requests.Add(1) {
		case 1:
			if strings.Contains(string(body), directory) {
				t.Error("directory present before explicit add")
			}
			response(w, "directory-before", "", nil, "BEFORE_DIRECTORY")
		case 2:
			if !strings.Contains(string(body), directory) || !strings.Contains(string(body), "Additional working directories") {
				t.Error("actual query context missing new permission directory", string(body))
			}
			response(w, "directory-write", "Write", map[string]any{"file_path": target, "content": "ACTUAL_ADDED_DIRECTORY_WRITE"}, "")
		case 4:
			if strings.Contains(string(obj(body).Get("system")), directory) {
				t.Error("removed directory retained in actual next system context")
			}
			response(w, "removed-directory-write", "Write", map[string]any{"file_path": target, "content": "MUST_REQUIRE_APPROVAL_AFTER_REMOVE"}, "")
		default:
			response(w, "directory-next", "", nil, "DIRECTORY_NEXT_DONE")
		}
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	o.Native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) {
		return nil, errors.New("disposable fixture keychain unavailable")
	}
	o.Interactive = true
	o.Arguments.SystemPrompt = ""
	// Source keeps default-mode Write approval even inside a working directory.
	// acceptEdits admits writes only when the live working-directory map includes
	// the target, so this proves the added path reaches the actual tool consumer.
	o.Arguments.PermissionMode = "acceptEdits"
	o.Arguments.Tools = []string{"Read,Write,Bash"}
	directory = filepath.Join(o.Native.Home, "external-workspace")
	target = filepath.Join(directory, "output.txt")
	_ = os.MkdirAll(directory, 0700)
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	_ = os.MkdirAll(filepath.Join(o.WorkDir, ".claude"), 0700)
	_ = os.MkdirAll(config, 0700)
	_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{"futureUser":{"keep":true}}`), 0600)
	local := filepath.Join(o.WorkDir, ".claude", "settings.local.json")
	_ = os.WriteFile(local, []byte(`{"futureLocal":{"keep":true},"permissions":{"allow":["Read"]}}`), 0600)
	s, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = s.Submit(ctx, tui.Input{Text: "Use the actual initial query context."}, nil); err != nil {
		t.Fatal(err)
	}
	var panel *tui.LocalPanel
	notice := ""
	closed := false
	emit := func(event tui.Event) error {
		if event.LocalPanel != nil {
			closed = false
			copy := *event.LocalPanel
			panel = &copy
		}
		if event.Kind == tui.LocalPanelClosed {
			closed = true
		}
		if event.Kind == tui.Notice {
			notice = event.Text
			if strings.HasPrefix(notice, "Added ") && !closed {
				t.Error("completion was displayed before its local dialog closed")
			}
		}
		return nil
	}
	if handled, err := s.InvokeTerminalCommand(ctx, "add-dir", directory+"/", emit); !handled || err != nil || len(panel.Rows) != 3 {
		t.Fatal(handled, err, panel)
	}
	id := panel.ID
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: id, Action: "directory-remember", Row: "remember"}, emit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "saved to local settings") {
		t.Fatal(notice, panel.Notice)
	}
	context, err := s.process.TerminalPanelSnapshot(ctx, "permissions")
	if err != nil {
		t.Fatal(err)
	}
	if entries := sdkpermissions.WorkspaceDirectoryEntries(context); len(entries) != 1 || entries[0].String("path") != directory {
		t.Fatal("actual permission context not mutated", string(raw(context)))
	}
	data, err := os.ReadFile(local)
	if err != nil || !strings.Contains(string(data), directory) || !strings.Contains(string(data), "futureLocal") || !strings.Contains(string(data), `"Read"`) {
		t.Fatal("real local settings merged incorrectly", err, string(data))
	}
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: id, Action: "directory-session"}, nil); err == nil {
		t.Fatal("closed directory dialog admitted stale side effect")
	}
	if err = s.Submit(ctx, tui.Input{Text: "Write the artifact in the directory I explicitly added."}, nil); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(target)
	if err != nil || string(data) != "ACTUAL_ADDED_DIRECTORY_WRITE" {
		t.Fatal("next native Write did not consume actual directory", err, string(data))
	}
	if requests.Load() != 3 || len(s.PendingPermissions()) != 0 {
		t.Fatal("local dialog sent model text or authorized directory asked again", requests.Load())
	}
	// Noargs Source form adds session access directly. It must not persist.
	other := filepath.Join(o.Native.Home, "other-workspace")
	_ = os.MkdirAll(other, 0700)
	before, _ := os.ReadFile(local)
	if _, err = s.InvokeTerminalCommand(ctx, "add-dir", "", emit); err != nil {
		t.Fatal(err)
	}
	if len(panel.Fields) != 1 {
		t.Fatal(panel)
	}
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: panel.ID, Action: "save", Fields: map[string]string{"path": other}}, emit); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(local)
	if string(after) != string(before) || !strings.Contains(notice, "for this session") {
		t.Fatal("session-only directory persisted", notice, string(after))
	}
	permission, err := s.process.TerminalPanelSnapshot(ctx, "permissions")
	if err != nil {
		t.Fatal(err)
	}
	source := ""
	for _, entry := range sdkpermissions.WorkspaceDirectoryEntries(permission) {
		if entry.String("path") == other {
			source = entry.String("source")
		}
	}
	if source != "session" {
		t.Fatal(string(raw(permission)))
	}
	if _, err = s.InvokeTerminalCommand(ctx, "add-dir", other, emit); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(notice, "already accessible") {
		t.Fatal("existing working-directory validation", notice)
	}
	var original map[string]any
	user, _ := os.ReadFile(filepath.Join(config, "settings.json"))
	_ = json.Unmarshal(user, &original)
	if len(original) != 1 || original["futureUser"] == nil {
		t.Fatal("user settings mutated", string(user))
	}
	if _, err = s.InvokeTerminalCommand(ctx, "permissions", "", emit); err != nil {
		t.Fatal(err)
	}
	workspaceID := panel.ID
	for _, result := range []tui.LocalPanelResult{{ID: workspaceID, Action: "workspace-open"}, {ID: workspaceID, Action: "workspace-remove", Row: directory}, {ID: workspaceID, Action: "workspace-remove-no"}} {
		if err = s.ApplyLocalPanel(ctx, result, emit); err != nil || panel.Notice != "" {
			t.Fatal(err, panel)
		}
	}
	if len(panel.Rows) != 3 {
		t.Fatal("canceled confirmation changed actual workspace", panel)
	}
	for _, result := range []tui.LocalPanelResult{{ID: workspaceID, Action: "workspace-remove", Row: directory}, {ID: workspaceID, Action: "workspace-remove-yes"}} {
		if err = s.ApplyLocalPanel(ctx, result, emit); err != nil {
			t.Fatal(err)
		}
	}
	permission, err = s.process.TerminalPanelSnapshot(ctx, "permissions")
	if err != nil || len(sdkpermissions.WorkspaceDirectoryEntries(permission)) != 1 {
		t.Fatal("actual workspace removal", err, string(raw(permission)))
	}
	retained, _ := os.ReadFile(local)
	if string(retained) != string(before) {
		t.Fatal("Source session-only removal edited saved workspace", string(retained))
	}
	if err = s.ApplyLocalPanel(ctx, tui.LocalPanelResult{ID: workspaceID, Action: "close"}, nil); err != nil {
		t.Fatal(err)
	}
	asked := make(chan tui.PermissionRequest, 1)
	done := make(chan error, 1)
	go func() {
		done <- s.Submit(ctx, tui.Input{Text: "Attempt a write after removing its workspace access."}, func(event tui.Event) error {
			if event.Kind == tui.PermissionAsked && event.Permission != nil {
				asked <- *event.Permission
			}
			return nil
		})
	}()
	select {
	case request := <-asked:
		if request.ToolName != "Write" {
			t.Fatal("workspace did not reach actual tool permission consumer", request)
		}
		if err = s.RespondPermission(ctx, request.ID, tui.PermissionDecision{Allowed: false}); err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("removed workspace Write did not ask", ctx.Err())
	}
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(target)
	if string(data) != "ACTUAL_ADDED_DIRECTORY_WRITE" || requests.Load() != 5 {
		t.Fatal("removed workspace bypassed real approval", string(data), requests.Load())
	}
}
