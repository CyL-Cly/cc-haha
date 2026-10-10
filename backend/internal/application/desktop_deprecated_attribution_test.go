package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestActualDesktopAndSharedCLIStartupAcceptRetiredAttributionWithoutPromptInjection(t *testing.T) {
	const retired = `{"attribution":{"commit":"RETIRED_COMMIT_NOT_IN_PROMPT","pr":"RETIRED_PR_NOT_IN_PROMPT","future":"raw-preserved"},"includeCoAuthoredBy":false,"future":{"keep":true}}`
	captured := []byte{}
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured, _ = io.ReadAll(r.Body)
		response(w, "retired-attribution", "", nil, "RETIRED_SETTINGS_OK")
	}))
	defer model.Close()
	o := nativeSessionOptions(t, model.URL)
	o.Arguments.Tools = nil
	o.Arguments.SystemPrompt = ""
	o.Arguments.PermissionMode = "default"
	config := o.Environment["CLAUDE_CONFIG_DIR"]
	if err := os.MkdirAll(config, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(config, "settings.json")
	if err := os.WriteFile(path, []byte(retired), 0600); err != nil {
		t.Fatal(err)
	}
	actual, err := NewDesktopServer(ServerOptions{Port: 60912, Environment: o.Environment, HomeDir: o.Environment["HOME"], ConfigDir: config, WorkDir: o.WorkDir, Version: o.Version, Arguments: o.Arguments, Stderr: io.Discard, RunSecurity: func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrPermission }, NativeFactory: func(env map[string]string, cwd, platform string) (*clibootstrap.NativeBoundary, error) {
		return clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: cwd, Platform: platform, ManagedRoot: o.Native.ManagedRoot, ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	}})
	if err != nil {
		t.Fatal("actual desktop startup rejected retired Source settings", err)
	}
	req := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:60912/health", nil)
	out := httptest.NewRecorder()
	actual.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal("actual ready endpoint", out.Code)
	}
	if err = actual.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	session, err := NewSession(context.Background(), o)
	if err != nil {
		t.Fatal("actual shared CLI startup rejected retired settings", err)
	}
	defer session.Close(context.Background())
	if err = session.Submit(context.Background(), tui.Input{Text: "Reply with RETIRED_SETTINGS_OK."}, nil); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(captured, []byte("You are an interactive agent")) || bytes.Contains(captured, []byte("RETIRED_COMMIT_NOT_IN_PROMPT")) || bytes.Contains(captured, []byte("RETIRED_PR_NOT_IN_PROMPT")) {
		t.Fatal("Source retired fields changed actual default prompt")
	}
	stored, err := os.ReadFile(path)
	if err != nil || string(stored) != retired {
		t.Fatal("startup rewrote user-owned legacy fields", err)
	}
}
