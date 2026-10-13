package clibootstrap

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestActualMainPrintCommittedTaskMailboxFailureUsesOwnedDiagnostic(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer peer.Close()
	var terminal bytes.Buffer
	m := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
		delete(n.Environment, "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC")
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"TeamCreate,TaskCreate,TaskUpdate,TaskGet"}
		o.Error = &terminal
	})
	created := callActualTeamTool(t, m, "TeamCreate", `{"team_name":"committed"}`)
	if created.String("team_name") != "committed" {
		t.Fatal(string(rawMain(created)))
	}
	callActualTeamTool(t, m, "TaskCreate", `{"subject":"committed task","description":"actual blocked mailbox"}`)
	blocker := filepath.Join(m.options.Native.Environment["CLAUDE_CONFIG_DIR"], "teams", "committed", "inboxes")
	if e := os.MkdirAll(filepath.Dir(blocker), 0700); e != nil {
		t.Fatal(e)
	}
	if e := os.WriteFile(blocker, []byte("existing file blocks inbox creation"), 0600); e != nil {
		t.Fatal(e)
	}
	updated := callActualTeamTool(t, m, "TaskUpdate", `{"taskId":"1","owner":"worker"}`)
	if string(updated.Get("success")) != "true" {
		t.Fatal(string(rawMain(updated)))
	}
	task, e := m.taskStore.Get("committed", quoteMain("1"))
	if e != nil || task == nil || task.String("owner") != "worker" {
		t.Fatalf("committed task %v error=%v", task, e)
	}
	if m.errorLog == nil || len(m.errorLog.Memory()) != 1 || !strings.Contains(m.errorLog.Memory()[0].Error, "inboxes") {
		t.Fatal("real task assignment failure was not logged through the native owner diagnostic")
	}
	if terminal.Len() != 0 || requests.Load() != 0 {
		t.Fatalf("assignment diagnostic leaked to terminal or called provider: %q requests=%d", terminal.String(), requests.Load())
	}
}
