package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestNativeSpinnerSettingProducesActualSpawnedTeammateTask(t *testing.T) {
	started := make(chan struct{}, 1)
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case started <- struct{}{}:
		default:
		}
		emitPlanFixture(w, 1, "", "PRIVATE_SPINNER_CHILD_DONE")
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		if err := os.WriteFile(filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "settings.json"), []byte(`{"enabledPlugins":{"sample@mkt":false},"spinnerVerbs":{"mode":"replace","verbs":["私人验证"]}}`), 0600); err != nil {
			t.Fatal(err)
		}
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,Read,SendMessage"}
	})
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"private-spinner"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var spinner string
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		_, tools, err := parent.nativeTools.Catalog(scope)
		if err != nil {
			return err
		}
		for _, tool := range tools {
			if tool.Name != "Agent" {
				continue
			}
			input := taskObject(t, []byte(`{"name":"worker","prompt":"finish the private spinner test","description":"private spinner"}`))
			future, err := tool.StartCallJSON(ctx, input, queryloop.JSONToolCallContext{ToolUseID: quoteMain("private-spinner-spawn"), Opaque: &builtinwrite.CallContext{Scope: scope}})
			if err != nil {
				return err
			}
			if _, err = future.Await(scope); err != nil {
				return err
			}
			tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
			for _, task := range tasks {
				if task.String("type") == "in_process_teammate" {
					spinner = task.String("spinnerVerb")
				}
			}
			return err
		}
		return mainCap("actual Agent tool")
	}); err != nil {
		t.Fatal(err)
	}
	if spinner != "私人验证" {
		t.Fatal("configured setting did not reach actual task state", spinner)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual spawned task never executed", ctx.Err())
	}
	if err := parent.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
