package queryhooks

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/httphook"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
)

func TestNativeHTTPStopAggregationMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	type row struct {
		Hook     protocol.JSONObject `json:"hook"`
		Response httphook.Result     `json:"response"`
	}
	rows := []row{}
	for _, body := range []string{"", `{}`, `{"continue":false,"stopReason":"prevented"}`, `{"decision":"block","reason":"block \ud800"}`, `{"continue":false,"decision":"block","reason":"both"}`, `{"systemMessage":"actual system note"}`, `{"async":true,"asyncTimeout":1000}`, "not JSON", `{"continue":`, `{"continue":"invalid"}`, `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny"}}`} {
		for _, display := range []string{"", "actual HTTP display"} {
			code := 200
			hook := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("http")}, protocol.JSONField{Name: "url", Value: commandJSON("http://127.0.0.1:1/original")})
			if display != "" {
				hook.Set("statusMessage", commandJSON(display))
			}
			rows = append(rows, row{hook, httphook.Result{OK: true, StatusCode: &code, Body: commandJSON(body)}})
		}
	}
	for _, response := range []httphook.Result{{OK: false, Body: commandJSON(""), Error: commandJSON("actual blocked policy")}, {OK: false, Body: commandJSON(""), Aborted: true}} {
		rows = append(rows, row{protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: commandJSON("http")}, protocol.JSONField{Name: "url", Value: commandJSON("http://127.0.0.1:1/original")}), response})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("testdata/http-stop.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_HTTP_STOP_FIXTURE=1", "PATH=/usr/bin:/bin", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1"}
	cmd.Stdin = bytes.NewReader(commandJSON(map[string]any{"home": home, "rows": rows}))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(output))
	}
	var source struct{ Rows []json.RawMessage }
	if err = json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		owner := cooperative.New()
		factory, _ := messages.NewFactory(func() string { return "fixture" }, func() time.Time { return time.Unix(0, 0) })
		s := &Service{options: Options{MainSessionID: "http-source-session", UUID: func() string { return "fixture" }, Commands: &CommandBindings{Owner: owner, Factory: factory, Timestamp: func() string { return "fixture" }, NowMillis: func() float64 { return 0 }, Input: func(context.Context, Context) (protocol.JSONObject, error) {
			return protocol.NewJSONObject(protocol.JSONField{Name: "session_id", Value: commandJSON("http-source-session")}), nil
		}}, HTTP: &HTTPBindings{Owner: owner, Run: func(context.Context, Context, protocol.JSONObject, protocol.JSONObject) (httphook.Result, error) {
			return row.Response, nil
		}}}}
		observed := []protocol.JSONObject{}
		var decision queryloop.StopDecision
		err = owner.Run(ctx, func(scope *cooperative.Scope) error {
			var failure error
			decision, failure = s.executeNativeCommandStop(ctx, Context{Scope: scope}, "Stop", "http-stop-id", []commandMatcher{{hook: row.Hook}}, func(value queryloop.Yield) error {
				if value.Message != nil {
					record := value.Message.Record.Clone()
					record.Delete("uuid")
					record.Delete("timestamp")
					observed = append(observed, record)
				}
				return nil
			})
			return failure
		})
		owner.Close()
		if err != nil {
			t.Fatal(i, err)
		}
		blocks := []protocol.JSONObject{}
		for _, m := range decision.BlockingMessages {
			record := m.Record.Clone()
			record.Delete("uuid")
			record.Delete("timestamp")
			blocks = append(blocks, record)
		}
		for _, record := range observed {
			if att, ok := record.Object("attachment"); ok && att.Get("durationMs") != nil {
				att.Set("durationMs", commandJSON(0))
				record.Set("attachment", commandJSON(att))
			}
		}
		actualRaw := commandJSON(map[string]any{"messages": observed, "result": map[string]any{"blockingErrors": blocks, "preventContinuation": decision.PreventContinuation}})
		var actual, expected any
		_ = json.Unmarshal(actualRaw, &actual)
		_ = json.Unmarshal(source.Rows[i], &expected)
		if !reflect.DeepEqual(actual, expected) || bytes.Count(actualRaw, []byte(`\ud800`)) != bytes.Count(source.Rows[i], []byte(`\ud800`)) {
			t.Errorf("Source HTTP Stop%d\nGo%s\nSource%s", i, actualRaw, source.Rows[i])
		}
	}
}
