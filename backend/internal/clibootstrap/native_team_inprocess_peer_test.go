package clibootstrap

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
)

func TestActualInProcessTeammatePeerResumesEvictedCheckpointInLeadGraph(t *testing.T) {
	var workerCalls, peerCalls atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		body := string(data)
		if strings.Contains(body, "PEER_MESSAGE_DRIVER") {
			if peerCalls.Add(1) == 1 {
				emitNativeTaskTool(w, "peer-resume", "SendMessage", map[string]string{"to": "worker", "message": "WAKE_FROM_PEER", "summary": "restore peer checkpoint"})
			} else {
				if !strings.Contains(body, "prior messages and your message as its next prompt") {
					t.Error("actual peer owner did not get resume result", body)
				}
				emitPlanFixture(w, 4, "", "PEER_RESUME_COMPLETE")
			}
			return
		}
		if workerCalls.Add(1) == 1 {
			emitPlanFixture(w, 1, "", "PERSISTENT_WORKER_RESULT")
			return
		}
		if !strings.Contains(body, "PERSISTENT_WORKER_RESULT") || !strings.Contains(body, "WAKE_FROM_PEER") || !strings.Contains(body, `teammate_id=\"messenger\"`) {
			t.Error("peer restore lost physical history or caller identity")
		}
		emitPlanFixture(w, 3, "", "RESTORED_FROM_PEER_RESULT")
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) { n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1" }, func(o *MainPrintOptions) { o.Arguments.Tools = []string{"Agent,TeamCreate,SendMessage,Read"} })
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"peer-checkpoint"}`)
	first := callActualTeamTool(t, parent, "Agent", `{"name":"worker","prompt":"WRITE_WORKER_HISTORY","description":"actual checkpoint worker"}`)
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	findIdle := func(name string) *sessioncontroller.BackgroundTask {
		t.Helper()
		for ctx.Err() == nil {
			task, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) {
				rows, err := parent.Controller.BackgroundTasksOnOwner(scope)
				if err != nil {
					return nil, err
				}
				for _, row := range rows {
					identity, _ := row.Object("identity")
					if identity.String("agentName") == name && row.String("status") == "running" && string(row.Get("isIdle")) == "true" {
						return parent.Controller.BackgroundTaskOnOwner(scope, row.String("id"))
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if task != nil {
				return task
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("actual peer never reached idle", name)
		return nil
	}
	original := findIdle("worker")
	originalIdentity, _ := original.Data.Object("identity")
	if killed, err := parent.KillTeammate(ctx, original.Data.String("id")); err != nil || !killed {
		t.Fatal(killed, err)
	}
	if err := original.Join(ctx); err != nil {
		t.Fatal(err)
	}
	callActualTeamTool(t, parent, "Agent", `{"name":"messenger","prompt":"PEER_MESSAGE_DRIVER","description":"actual peer sender"}`)
	findIdle("messenger")
	restored := findIdle("worker")
	identity, _ := restored.Data.Object("identity")
	if identity.String("agentId") != first.String("agent_id") || identity.String("resumableAgentId") != originalIdentity.String("resumableAgentId") || original.Data.String("id") == restored.Data.String("id") {
		t.Fatal("peer created unrelated teammate", restored.Data)
	}
	if workerCalls.Load() != 2 || peerCalls.Load() != 2 {
		t.Fatal("resume did not use exactly actual two owners", workerCalls.Load(), peerCalls.Load())
	}
	if err := parent.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
}
