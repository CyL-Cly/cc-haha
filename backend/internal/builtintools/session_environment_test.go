package builtintools

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionenvironment"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolerrors"
)

func TestSessionEnvironmentOriginalBashEvalStageReadsPhysicalFilesMemoAndShortCircuits(t *testing.T) {
	s, owner, home, _ := fixture(t)
	service, err := sessionenvironment.New(filepath.Join(home, "config"), s.options.Platform)
	if err != nil {
		t.Fatal(err)
	}
	path, err := service.HookEnvFile("actual-sid", "SessionStart", 0)
	if err != nil {
		t.Fatal(err)
	}
	script := "export HOOK_VALUE='original hook value'\nshopt -s expand_aliases\nalias hook_probe='printf alias-result'"
	if err = os.WriteFile(path, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	s.options.SessionEnvironmentScript = func(ctx context.Context, scope *cooperative.Scope) (*string, error) {
		future, err := native(s, scope, func() (*string, error) { return service.Script("actual-sid", s.options.Environment) })
		if err != nil {
			return nil, err
		}
		return future.Await(scope)
	}
	result := call(t, s, owner, "Bash", input(`{"command":"hook_probe; printf ':%s' \"$HOOK_VALUE\""}`))
	var value protocol.JSONObject
	_ = value.UnmarshalJSON(result)
	if value.String("stdout") != "alias-result:original hook value\n" {
		t.Fatal("actual environment/alias eval", string(result))
	}
	if err = os.WriteFile(path, []byte("export HOOK_VALUE='changed'"), 0600); err != nil {
		t.Fatal(err)
	}
	result = call(t, s, owner, "Bash", input(`{"command":"printf '%s' \"$HOOK_VALUE\""}`))
	_ = value.UnmarshalJSON(result)
	if value.String("stdout") != "original hook value\n" {
		t.Fatal("process memo was ignored", string(result))
	}
	service.Invalidate()
	result = call(t, s, owner, "Bash", input(`{"command":"printf '%s' \"$HOOK_VALUE\""}`))
	_ = value.UnmarshalJSON(result)
	if value.String("stdout") != "changed\n" {
		t.Fatal("memo invalidation missing", string(result))
	}
	if err = os.WriteFile(path, []byte("false"), 0600); err != nil {
		t.Fatal(err)
	}
	service.Invalidate()
	target := filepath.Join(home, "must-not-write")
	err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		_, failure := s.Call(context.Background(), "Bash", protocol.NewJSONObject(field("command", "printf data > '"+target+"'")), scope, false)
		return failure
	})
	var shell *toolerrors.ShellError
	if err != nil && !errors.As(err, &shell) {
		t.Fatal(err)
	}
	if _, err = os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("failed original env stage executed raw command", err)
	}
}
