package application

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/workflow"
)

// Definitions use an API parsing actor and borrow the process settings/global
// writer. There is no conversation, inference loop or Agent in this producer.
func newDesktopWorkflows(o ServerOptions, settings *clibootstrap.NativeProcessSettings, budget *sessionstore.Budget, notifications func(context.Context, string) ([]protocol.JSONObject, error)) (*workflow.Service, func(context.Context) error, error) {
	base := o.Environment["CLAUDE_CODE_TMPDIR"]
	if base == "" {
		base = "/tmp"
		if runtime.GOOS == "windows" {
			base = os.TempDir()
		}
	}
	if resolved, err := filepath.EvalSymlinks(base); err == nil {
		base = resolved
	}
	name := "claude-" + strconv.Itoa(os.Getuid())
	if runtime.GOOS == "windows" {
		name = "claude"
	}
	history, err := workflow.NewHistory(workflow.HistoryOptions{ConfigDir: o.ConfigDir, TempDir: filepath.Join(base, name), Files: workflow.NativeHistoryFiles(), Reader: sessionstore.NewReaderWithBudget(nil, budget), Notifications: notifications})
	if err != nil {
		return nil, nil, err
	}
	owner := cooperative.New()
	service, err := settings.WorkflowDefinitions(owner, o.WorkDir, history)
	if err != nil {
		owner.Close()
		return nil, nil, err
	}
	return service, func(context.Context) error { owner.Close(); return nil }, nil
}
