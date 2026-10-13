package sdkhost

import (
	"math"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/querypolicy"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
)

type taskBudgetTracker struct {
	total     *float64
	remaining *float64
}

func (h *Host) newTaskBudgetTracker() *taskBudgetTracker {
	tracker := &taskBudgetTracker{}
	if !h.isAgent() && h.options.Print != nil && !h.options.Print.NativeInteractive {
		tracker.total = h.options.Print.TaskBudget
	}
	return tracker
}
func (tracker *taskBudgetTracker) RequestBudget() *requestcontext.TaskBudget {
	if tracker.total == nil {
		return nil
	}
	budget := &requestcontext.TaskBudget{Total: *tracker.total}
	if tracker.remaining != nil {
		remaining := *tracker.remaining
		budget.Remaining = &remaining
	}
	return budget
}
func (tracker *taskBudgetTracker) Compacted(history []queryloop.Message) error {
	if tracker.total == nil {
		return nil
	}
	rows := make([]protocol.JSONObject, 0, len(history))
	for _, message := range history {
		raw, err := message.MarshalJSON()
		if err != nil {
			return err
		}
		var record protocol.JSONObject
		if err := record.UnmarshalJSON(raw); err != nil {
			return err
		}
		rows = append(rows, record)
	}
	window, err := querypolicy.FinalTaskBudgetContext(rows)
	if err != nil {
		return err
	}
	remaining := *tracker.total
	if tracker.remaining != nil {
		remaining = *tracker.remaining
	}
	remaining = math.Max(0, remaining-window)
	tracker.remaining = &remaining
	return nil
}
