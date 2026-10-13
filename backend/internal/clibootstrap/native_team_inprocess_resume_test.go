package clibootstrap

import (
	"context"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
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

func TestActualStoppedEvictedTeammateResumesSamePhysicalTranscriptAndMetadataWithoutUUIDDuplicates(t *testing.T) {
	var posts atomic.Int32
	target := ""
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		switch round := posts.Add(1); round {
		case 1:
			emitNativeTaskTool(w, "checkpoint-write", "Write", map[string]string{"file_path": target, "content": "PHYSICAL_TEAMMATE_CHECKPOINT"})
		case 2:
			emitPlanFixture(w, 2, "", "FIRST_DURABLE_TEAMMATE_RESULT")
		case 3:
			if !strings.Contains(string(body), "FIRST_DURABLE_TEAMMATE_RESULT") || !strings.Contains(string(body), "continue from stopped checkpoint") {
				t.Error("actual restored request lost own history/prompt")
			}
			emitNativeTaskTool(w, "checkpoint-read", "Read", map[string]string{"file_path": target})
		case 4:
			if !strings.Contains(string(body), "PHYSICAL_TEAMMATE_CHECKPOINT") {
				t.Error("actual resumed Read did not consume physical artifact")
			}
			emitPlanFixture(w, 4, "", "RESUMED_DURABLE_TEAMMATE_RESULT")
		default:
			t.Error("extra provider request", round)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer peer.Close()
	parent := backgroundAgentMainWithOptions(t, peer.URL, func(n *NativeBoundary) {
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
		n.Environment["CLAUDE_CODE_ENABLE_TASKS"] = "1"
	}, func(o *MainPrintOptions) {
		o.Arguments.Tools = []string{"Agent,TeamCreate,TeamDelete,SendMessage,Read,Write"}
		o.Arguments.PermissionMode = "bypassPermissions"
	})
	target = filepath.Join(parent.workingDirectory(), "durable-checkpoint.txt")
	callActualTeamTool(t, parent, "TeamCreate", `{"team_name":"durable-resume"}`)
	spawn := callActualTeamTool(t, parent, "Agent", `{"name":"worker","prompt":"write physical checkpoint and report","description":"durable fixture"}`)
	id := spawn.String("agent_id")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	waitTask := func(round int32) *sessioncontroller.BackgroundTask {
		t.Helper()
		for ctx.Err() == nil {
			task, err := inProcessParentCall(ctx, parent, func(scope *cooperative.Scope) (*sessioncontroller.BackgroundTask, error) {
				tasks, err := parent.Controller.BackgroundTasksOnOwner(scope)
				if err != nil {
					return nil, err
				}
				for _, row := range tasks {
					identity, _ := row.Object("identity")
					if identity.String("agentId") == id && row.String("status") == "running" && string(row.Get("isIdle")) == "true" {
						return parent.Controller.BackgroundTaskOnOwner(scope, row.String("id"))
					}
				}
				return nil, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if task != nil && posts.Load() == round {
				return task
			}
			time.Sleep(5 * time.Millisecond)
		}
		t.Fatal("actual teammate did not reach idle", posts.Load())
		return nil
	}
	first := waitTask(2)
	identity, _ := first.Data.Object("identity")
	resumable := identity.String("resumableAgentId")
	var path string
	if err := parent.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var err error
		path, _, _, err = parent.Controller.AgentTranscriptSourceOnOwner(scope, resumable)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if killed, err := parent.KillTeammate(ctx, first.Data.String("id")); err != nil || !killed {
		t.Fatal(killed, err)
	}
	if err := first.Join(ctx); err != nil {
		t.Fatal(err)
	}
	metaPath := strings.TrimSuffix(path, ".jsonl") + ".meta.json"
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	meta := objMain(data)
	if meta.String("taskKind") != "in_process_teammate" || meta.String("name") != "worker" || meta.String("teamName") != "durable-resume" {
		t.Fatal("real checkpoint metadata missing", meta)
	}
	meta.Set("permissionMode", quoteMain("bypassPermissions"))
	meta.Set("unknownFutureField", rawMain(map[string]bool{"preserved": true}))
	if err := os.WriteFile(metaPath, rawMain(meta), 0600); err != nil {
		t.Fatal(err)
	}
	resumed, _ := taskToolCall(t, parent, "SendMessage", `{"to":"worker","message":"continue from stopped checkpoint","summary":"resume physical work"}`)
	if string(resumed.Get("success")) != "true" || !strings.Contains(resumed.String("message"), "prior messages") {
		t.Fatal("real SendMessage did not resume evicted teammate", resumed)
	}
	second := waitTask(4)
	secondIdentity, _ := second.Data.Object("identity")
	if secondIdentity.String("resumableAgentId") != resumable || second.Data.String("id") == first.Data.String("id") || second.Data.String("permissionMode") == "bypassPermissions" {
		t.Fatal("resume lost durable identity or trusted disk permission", second.Data)
	}
	if err := parent.ShutdownSession(ctx); err != nil {
		t.Fatal(err)
	}
	data, err = os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		var row protocol.JSONObject
		if err := row.UnmarshalJSON([]byte(line)); err != nil {
			t.Fatal(err)
		}
		uuid := row.String("uuid")
		if uuid != "" {
			if seen[uuid] {
				t.Fatal("physical UUID duplicated across checkpoint resume", uuid)
			}
			seen[uuid] = true
		}
	}
	if strings.Count(string(data), "FIRST_DURABLE_TEAMMATE_RESULT") != 1 || !strings.Contains(string(data), "RESUMED_DURABLE_TEAMMATE_RESULT") {
		t.Fatal("physical transcript replaced or replayed history", string(data))
	}
	data, err = os.ReadFile(metaPath)
	if err != nil {
		t.Fatal(err)
	}
	meta = objMain(data)
	var extra map[string]bool
	_ = json.Unmarshal(meta.Get("unknownFutureField"), &extra)
	if !extra["preserved"] || meta.String("permissionMode") != "default" {
		t.Fatal("metadata migration dropped unknown fields or restored bypass", meta)
	}
	if posts.Load() != 4 {
		t.Fatal("cleanup/restore busy polled", posts.Load())
	}
}
