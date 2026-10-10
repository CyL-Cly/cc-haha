package preflightcore

import (
	"bytes"
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestRetainedAttachmentEstimationAgainstActualSourceWithUsageAnchors(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun unavailable")
	}
	user := `{"type":"user","message":{"content":"a human turn"}}`
	full := `{"type":"attachment","attachment":{"type":"plan_mode","reminderType":"full","isSubAgent":false,"planFilePath":"/fixture/plans/actual.md","planExists":false}}`
	sparse := `{"type":"attachment","attachment":{"type":"plan_mode","reminderType":"sparse","planFilePath":"/fixture/plans/actual.md"}}`
	usage := `{"type":"assistant","message":{"id":"response-a","model":"deepseek-flash","content":[{"type":"text","text":"ready"}],"usage":{"input_tokens":20,"output_tokens":1}}}`
	sibling := `{"type":"assistant","message":{"id":"response-a","model":"deepseek-flash","content":[{"type":"text","text":"split"}],"usage":{"input_tokens":0,"output_tokens":0}}}`
	zeroOutput := `{"type":"assistant","message":{"id":"response-a","model":"deepseek-flash","content":[{"type":"text","text":"unreported"}],"usage":{"input_tokens":20,"output_tokens":0}}}`
	histories := [][]string{{user, full}, {full, usage}, {usage, full}, {sibling, full, usage}, {sibling, full, zeroOutput}, {full, sibling}, {user, sparse}, {user, full, sparse}, {`{"type":"attachment","attachment":null}`}, {`{"type":"attachment"}`}, {`{"type":"attachment","attachment":false}`}, {`{"type":"attachment","attachment":{"type":"skill_listing","content":"skill content"}}`}, {`{"type":"attachment","attachment":{"type":"invoked_skills","skills":[{"name":"n","path":"/fixture/p","content":"instructions"}]}}`}, {`{"type":"attachment","attachment":{"type":"invoked_skills","skills":null}}`}}
	for _, kind := range []string{"plan_mode_reentry", "plan_mode_exit"} {
		for _, exists := range []bool{true, false} {
			for _, p := range []string{`"/fixture/p.md"`, `"\ud800__FILE_INFO__\udfff"`, `null`} {
				histories = append(histories, []string{user, `{"type":"attachment","attachment":{"type":"` + kind + `","planFilePath":` + p + `,"planExists":` + string(rawBool(exists)) + `}}`})
			}
		}
	}
	rows := []map[string]any{}
	for _, h := range histories {
		rows = append(rows, map[string]any{"history": "[" + joinSources(h) + "]", "options": map[string]any{}})
	}
	allocation := t.TempDir()
	home := filepath.Join(allocation, "home")
	_ = os.MkdirAll(filepath.Join(home, "tmp"), 0700)
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-plan.fixture.ts")
	data, _ := json.Marshal(map[string]any{"homeDir": home, "inputs": []any{}, "estimates": rows})
	cmd := exec.Command(bun, "--no-env-file", "--feature=TRANSCRIPT_CLASSIFIER", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + allocation, "CC_HAHA_NATIVE_PLAN_FIXTURE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=false", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("actual Source %v %.3000s", e, out)
	}
	type observed struct {
		Value *float64                        `json:"value"`
		Error *struct{ Name, Message string } `json:"error"`
	}
	var source struct {
		Estimates    []struct{ Tokens, Rough observed }
		Capabilities struct{ Blocked []string }
	}
	if e := json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	if len(source.Estimates) != len(histories) || len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source did not exercise expected estimator rows", source)
	}
	for i, h := range histories {
		input := mustOriginal(t, h...)
		ids, dates := 0, 0
		factory, _ := messages.NewFactory(func() string { ids++; return "estimation-uuid" }, func() time.Time { dates++; return time.UnixMilli(1700000000000) })
		options := &AttachmentOptions{Factory: factory, Plan: &attachments.PlanOptions{ExplorePlanAgentsEnabled: true}}
		check := func(name string, got float64, err error, want observed) {
			t.Helper()
			if want.Error != nil {
				if err == nil || err.Error() != want.Error.Message {
					t.Fatalf("row %d %s Source=%+v Go=%v", i, name, want.Error, err)
				}
			} else if err != nil || want.Value == nil || got != *want.Value {
				t.Fatalf("row %d %s Source=%+v Go=%v error=%v", i, name, want.Value, got, err)
			}
		}
		value, err := EstimateContextWithAttachments(input, options)
		check("tokenCountWithEstimation", value, err, source.Estimates[i].Tokens)
		if i == 1 && (ids != 0 || dates != 0) {
			t.Fatal("attachment before actual usage anchor consumed UUID/clock", ids, dates)
		}
		ids, dates = 0, 0
		value, err = RoughHistoryWithAttachments(input, options)
		check("rough estimation", value, err, source.Estimates[i].Rough)
		if i == 0 && (ids != 1 || dates != 1) {
			t.Fatal("rough slice did not use actual shared allocation producer", ids, dates)
		}
	}
}
func joinSources(rows []string) string {
	var out string
	for i, row := range rows {
		if i > 0 {
			out += ","
		}
		out += row
	}
	return out
}
func rawBool(value bool) []byte {
	if value {
		return []byte("true")
	}
	return []byte("false")
}
