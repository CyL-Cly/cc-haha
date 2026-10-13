package sdkhost

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"testing"
)

func TestTaskBudgetRemainsPerASKAndCompactionUsesActualPriorResponse(t *testing.T) {
	total := 40.
	host := &Host{options: Options{Print: &PrintOptions{TaskBudget: &total}}}
	tracker := host.newTaskBudgetTracker()
	first := tracker.RequestBudget()
	if first == nil || first.Remaining != nil || first.Total != 40 {
		t.Fatal(first)
	}
	var record protocol.JSONObject
	if err := json.Unmarshal([]byte(`{"type":"assistant","uuid":"actual-response","message":{"role":"assistant","content":[{"type":"text","text":"Actual response"}],"model":"actual-model","usage":{"input_tokens":2000,"output_tokens":40,"cache_read_input_tokens":8000,"iterations":[{"input_tokens":900,"output_tokens":99},{"input_tokens":17,"output_tokens":5}]}}}`), &record); err != nil {
		t.Fatal(err)
	}
	message := queryloop.Message{Record: record}
	if err := tracker.Compacted([]queryloop.Message{message}); err != nil {
		t.Fatal(err)
	}
	remaining := tracker.RequestBudget()
	if remaining.Remaining == nil || *remaining.Remaining != 18 {
		t.Fatal("final server iteration / cache exclusion", remaining)
	}
	*remaining.Remaining = 999
	if *tracker.RequestBudget().Remaining != 18 {
		t.Fatal("request changed retained budget")
	}
	if err := tracker.Compacted([]queryloop.Message{message}); err != nil {
		t.Fatal(err)
	}
	if *tracker.RequestBudget().Remaining != 0 {
		t.Fatal("negative remaining was not clamped")
	}
	if host.newTaskBudgetTracker().RequestBudget().Remaining != nil {
		t.Fatal("next ASK inherited compacted remaining")
	}
	host.options.Print.AgentID = "real-child"
	if host.newTaskBudgetTracker().RequestBudget() != nil {
		t.Fatal("parent print task budget leaked into Agent query")
	}
}
