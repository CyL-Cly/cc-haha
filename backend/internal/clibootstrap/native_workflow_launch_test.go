package clibootstrap

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualWorkflowLaunchUniqueTaskPhysicalScriptOutputAndCachedResume(t *testing.T) {
	var requests atomic.Int32
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = requests.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
		emitPlanFixture(w, 1, "", "PHYSICAL_WORKFLOW_RETURN")
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var launched protocol.JSONObject
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		launched, e = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(`export const meta={name:'launch-native',description:'Actual native worker',phases:[{title:'inspect'}]};phase('inspect');return await agent('Read the private fixture and return its answer')`)}), quoteMain("actual-workflow-tool-use"))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	wait := func(id string) protocol.JSONObject {
		var terminal protocol.JSONObject
		if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
			entry, e := m.Controller.BackgroundTaskOnOwner(scope, id)
			if e != nil {
				return e
			}
			if entry == nil {
				t.Fatal("actual task missing")
			}
			future, e := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, entry.Join(ctx) })
			if e != nil {
				return e
			}
			if _, e = future.Await(scope); e != nil {
				return e
			}
			entry, e = m.Controller.BackgroundTaskOnOwner(scope, id)
			if e != nil {
				return e
			}
			terminal = entry.Data
			return nil
		}); e != nil {
			t.Fatal(e)
		}
		return terminal
	}
	terminal := wait(launched.String("taskId"))
	if terminal.String("type") != "local_workflow" || terminal.String("status") != "completed" || terminal.String("result") != "PHYSICAL_WORKFLOW_RETURN" || terminal.String("notified") != "" && string(terminal.Get("notified")) != "true" {
		t.Fatalf("real workflow did not settle %s", rawMain(terminal))
	}
	script, e := os.ReadFile(launched.String("scriptPath"))
	if e != nil || !strings.Contains(string(script), "launch-native") {
		t.Fatal(e, string(script))
	}
	data, e := os.ReadFile(terminal.String("outputFile"))
	if e != nil {
		t.Fatal(e)
	}
	var output protocol.JSONObject
	if e = output.UnmarshalJSON(data); e != nil || output.String("result") != "PHYSICAL_WORKFLOW_RETURN" || string(output.Get("agentCount")) != "1" || string(output.Get("totalToolCalls")) != "0" {
		t.Fatalf("lost real disk output %s %v", data, e)
	}
	var replay protocol.JSONObject
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		replay, e = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "scriptPath", Value: launched.Get("scriptPath")}, protocol.JSONField{Name: "resumeFromRunId", Value: launched.Get("runId")}), quoteMain("resume-tool"))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	resumed := wait(replay.String("taskId"))
	if resumed.String("status") != "completed" || requests.Load() != 1 || replay.String("runId") != launched.String("runId") {
		t.Fatalf("physical cache resume reran model %s requests%d", rawMain(resumed), requests.Load())
	}
	if e = m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		tasks, e := m.Controller.BackgroundTasksOnOwner(scope)
		if e != nil {
			return e
		}
		if len(tasks) != 1 || tasks[0].String("id") != replay.String("taskId") {
			return fmt.Errorf("resumed run left duplicate/ordinary Agent tasks %s", rawMain(tasks))
		}
		queued, e := m.Controller.HasQueuedTaskNotificationsOnOwner(scope)
		if e != nil {
			return e
		}
		if !queued {
			return fmt.Errorf("missing original later-priority model notification")
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
}

func TestActualWorkflowStopCancelsAndJoinsPhysicalModelBeforeTeardown(t *testing.T) {
	started := make(chan struct{})
	canceled := make(chan struct{})
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(canceled)
	}))
	defer peer.Close()
	m := backgroundAgentMainWithSetup(t, peer.URL, func(n *NativeBoundary) { delete(n.Environment, "CLAUDE_CODE_SIMPLE") })
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var launched protocol.JSONObject
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		var e error
		launched, e = m.launchNativeWorkflow(ctx, scope, protocol.NewJSONObject(protocol.JSONField{Name: "script", Value: quoteMain(`export const meta={name:'stop-native',description:'Actual native cancellation'};return await agent('Wait for actual cancellation')`)}), quoteMain("stop-tool"))
		return e
	}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("actual worker never opened physical model")
	}
	if e := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, e := m.Controller.StopBackgroundTaskOnOwner(scope, launched.String("taskId"), m.options.Native.NowMillis()); e != nil {
			return e
		}
		entry, e := m.Controller.BackgroundTaskOnOwner(scope, launched.String("taskId"))
		if e != nil {
			return e
		}
		future, e := nativeSkillWork(m, scope, func() (struct{}, error) { return struct{}{}, entry.Join(ctx) })
		if e != nil {
			return e
		}
		if _, e = future.Await(scope); e != nil {
			return e
		}
		entry, e = m.Controller.BackgroundTaskOnOwner(scope, launched.String("taskId"))
		if e != nil {
			return e
		}
		if entry.Data.String("status") != "killed" || string(entry.Data.Get("notified")) != "true" {
			t.Fatalf("stop overwritten by later worker %s", rawMain(entry.Data))
		}
		return nil
	}); e != nil {
		t.Fatal(e)
	}
	select {
	case <-canceled:
	case <-ctx.Done():
		t.Fatal("task stop did not cancel physical request")
	}
}
