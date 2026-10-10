package scheduler

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestSchedulerCronOutputAndTimeoutMatchActualSource(t *testing.T) {
	fields := []map[string]any{}
	for _, field := range []string{"*", "*/2", "1,3,5", "1-5", "1-10/3", "5/2", "0/1", "*/0", "bad", "5junk", "+5", "-1", "1-2-3", "5/2/7", " 5 ", "1, 3", "1-x/2", "*/1e2"} {
		for value := 0; value < 12; value++ {
			fields = append(fields, map[string]any{"field": field, "value": value})
		}
	}
	cron := []map[string]string{}
	for _, expr := range []string{"* * * * *", "*/2 12 * * *", "0 12 1 * 1", "0 12 * * 7", "0 12 * * 0", "0 12 * * * extra", "\ufeff0 12 1 6 1\ufeff", "5junk 12 * * *"} {
		for _, date := range []string{"2026-06-01T12:00:00Z", "2026-06-07T12:05:00Z"} {
			cron = append(cron, map[string]string{"cron": expr, "date": date})
		}
	}
	text := []string{"", "junk\n", `{"type":"assistant","message":{"content":[{"type":"thinking","thinking":"hidden"},{"type":"text","text":"  first  "},{"type":"tool_use","name":"Read"},{"type":"text","text":"second"}]}}` + "\n" + `{"type":"result","result":"first\n\nsecond"}`, `{"type":"assistant","message":{"content":[{"type":"text","text":"first"}]}}` + "\n" + `{"type":"result","result":"first"}`, `{"type":"result","result":{"message":" object result "}}`, `{"type":"assistant","message":{"content":[{"type":"text","text":"\ud800 \ud83d\ude00"}]}}`}
	timeouts := []string{"", " 10 ", "0", "-1", "1.5", "1e3", "0x10", "0b10", "0o10", "Infinity", "2147483648", "9007199254740992", "junk"}
	data := actualSource(t, "semantics", map[string]any{"fields": fields, "cron": cron, "text": text, "timeouts": timeouts})
	var result struct {
		Fields, Cron []bool
		Text         []json.RawMessage
		Timeouts     []float64
	}
	if e := json.Unmarshal(data, &result); e != nil {
		t.Fatal(e)
	}
	for i, row := range fields {
		if actual := FieldMatches(row["field"].(string), row["value"].(int)); actual != result.Fields[i] {
			t.Errorf("field%d %v Go%v Source%v", i, row, actual, result.Fields[i])
		}
	}
	for i, row := range cron {
		date, _ := time.Parse(time.RFC3339Nano, row["date"])
		if actual := CronMatches(row["cron"], date); actual != result.Cron[i] {
			t.Errorf("cron%d %v Go%v Source%v", i, row, actual, result.Cron[i])
		}
	}
	for i, row := range text {
		actual := extractAssistantText(row)
		if string(protocol.StringifyValidatedJavaScriptJSON(actual)) != string(protocol.StringifyValidatedJavaScriptJSON(result.Text[i])) {
			t.Errorf("output%d Go%s Source%s", i, actual, result.Text[i])
		}
	}
	for i, row := range timeouts {
		if actual := ResolveTaskTimeoutMs(map[string]string{"CC_HAHA_TASK_TIMEOUT_MS": row}); actual != result.Timeouts[i] {
			t.Errorf("timeout %s Go%s Source%s", row, fmt.Sprint(actual), fmt.Sprint(result.Timeouts[i]))
		}
	}
}
