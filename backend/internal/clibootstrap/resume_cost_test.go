package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/projectpath"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// This old fixture is original persisted user input, not Source output or a
// prepared Controller state. The public SID loader must read its JSONL itself.
func TestPublicResumeRestoresStoredCostWithoutWritingOldConfig(t *testing.T) {
	testPublicResumeRestoresStoredCost(t, nativeForTest(t))
}

func TestNativeLongProjectResumeRestoresStoredCostAndKeepsOriginalFiles(t *testing.T) {
	base := nativeForTest(t)
	work := filepath.Join(base.CWD, strings.Repeat("project-component-", 6), strings.Repeat("nested-component-", 6))
	if err := os.MkdirAll(work, 0700); err != nil {
		t.Fatal(err)
	}
	if len(work) <= 200 {
		t.Fatal("regression must exercise the original long-path hash threshold", work)
	}
	native, err := newIsolatedProcessNativeBoundary(NativeProcessOptions{Environment: base.Environment, CWD: work, Platform: "darwin", ManagedRoot: filepath.Join(base.Temp, "managed"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	if err != nil {
		t.Fatal(err)
	}
	testPublicResumeRestoresStoredCost(t, native)
}

func testPublicResumeRestoresStoredCost(t *testing.T, n *NativeBoundary) {
	t.Helper()
	const sid = "777356f4-e5f6-4b45-85bb-23c48db5b4cf"
	fixture, e := os.ReadFile("testdata/old-project-cost.json")
	if e != nil {
		t.Fatal(e)
	}
	fixture = bytes.ReplaceAll(fixture, []byte("${PROJECT}"), []byte(n.CWD))
	config := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], ".claude.json")
	if e = os.MkdirAll(filepath.Dir(config), 0700); e != nil {
		t.Fatal(e)
	}
	if e = os.WriteFile(config, fixture, 0600); e != nil {
		t.Fatal(e)
	}
	path := filepath.Join(n.Environment["CLAUDE_CONFIG_DIR"], "projects", projectpath.Sanitize(n.CWD), sid+".jsonl")
	if e = os.MkdirAll(filepath.Dir(path), 0700); e != nil {
		t.Fatal(e)
	}
	rows := []map[string]any{
		{"type": "user", "uuid": "10000000-0000-4000-8000-000000000001", "timestamp": "2023-11-14T22:13:20.000Z", "cwd": n.CWD, "sessionId": sid, "parentUuid": nil, "message": map[string]any{"role": "user", "content": "Earlier persisted request"}, "unknownOuter": map[string]any{"keep": true}},
		{"type": "assistant", "uuid": "10000000-0000-4000-8000-000000000002", "timestamp": "2023-11-14T22:13:20.001Z", "cwd": n.CWD, "sessionId": sid, "parentUuid": "10000000-0000-4000-8000-000000000001", "message": map[string]any{"role": "assistant", "model": "fixture-model", "content": []any{map[string]any{"type": "text", "text": "Earlier persisted answer"}}, "usage": map[string]any{"input_tokens": 3750, "output_tokens": 0}, "stop_reason": "end_turn"}},
	}
	var log bytes.Buffer
	for _, row := range rows {
		b, e := json.Marshal(row)
		if e != nil {
			t.Fatal(e)
		}
		log.Write(b)
		log.WriteByte('\n')
	}
	if e = os.WriteFile(path, log.Bytes(), 0600); e != nil {
		t.Fatal(e)
	}
	a := mainRecipeArgs()
	a[len(a)-2] = "--resume"
	a[len(a)-1] = sid
	for i := range a {
		if a[i] == "--tools" {
			a[i+1] = ""
		}
	}
	args, e := ParsePrintArguments(a, "fixture")
	if e != nil {
		t.Fatal(e)
	}
	var out, errOut bytes.Buffer
	p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"/cost\"}}\n"), Output: &out, Error: &errOut})
	if e != nil {
		t.Fatal(e)
	}
	defer p.Close()
	// Native construction now runs main's real first-start/migration prefix.
	// Check its exact additive fields, then require resume and /cost itself to
	// leave every byte of that initialized persisted config untouched.
	beforeResume, e := os.ReadFile(config)
	if e != nil {
		t.Fatal(e)
	}
	if n.NativeProcess {
		var old, initialized map[string]any
		if json.Unmarshal(fixture, &old) != nil || json.Unmarshal(beforeResume, &initialized) != nil {
			t.Fatal("invalid original or initialized config")
		}
		stamp, ok := initialized["firstStartTime"].(string)
		if _, err := time.Parse(time.RFC3339Nano, stamp); !ok || err != nil || initialized["migrationVersion"] != float64(11) || initialized["opusProMigrationComplete"] != true || initialized["sonnet1m45MigrationComplete"] != true {
			t.Fatal("actual main prefix did not apply the original startup fields", initialized)
		}
		for _, key := range []string{"firstStartTime", "migrationVersion", "opusProMigrationComplete", "sonnet1m45MigrationComplete"} {
			delete(initialized, key)
		}
		if !reflect.DeepEqual(old, initialized) {
			t.Fatal("actual startup changed protected old config/unknown fields", old, initialized)
		}
	} else if !bytes.Equal(beforeResume, fixture) {
		t.Fatal("non-native construction changed original persisted config")
	}
	code, e := p.Run(context.Background())
	if e != nil || code != 0 {
		t.Fatal(code, e, errOut.String(), out.String())
	}
	var localText string
	var usage map[string]map[string]json.RawMessage
	for _, line := range bytes.Split(out.Bytes(), []byte("\n")) {
		var v struct {
			Type       string
			Result     string
			ModelUsage map[string]map[string]json.RawMessage
		}
		if json.Unmarshal(line, &v) == nil && v.Type == "result" {
			localText = v.Result
			usage = v.ModelUsage
		}
	}
	if !strings.HasPrefix(localText, "Total cost:            $0.0313\n") || !strings.Contains(localText, "Total code changes:    7 lines added, 2 lines removed") || !strings.Contains(localText, "3.8k input") || strings.Contains(localText, "inaccurate") {
		t.Fatalf("public resume lost original persisted nonzero cost: %q", localText)
	}
	if !strings.Contains(localText, "Total duration (API):  2s") {
		t.Fatal(localText)
	}
	var future struct{ Keep []int }
	_ = json.Unmarshal(usage["fixture-model"]["futureUsage"], &future)
	if len(future.Keep) != 2 || string(usage["fixture-model"]["contextWindow"]) != "200000" || string(usage["fixture-model"]["maxOutputTokens"]) != "32000" {
		t.Fatalf("public result lost old unknown row or refreshed metadata: %#v", usage)
	}
	transcriptAfter, readErr := os.ReadFile(path)
	if readErr != nil || !bytes.HasPrefix(transcriptAfter, log.Bytes()) {
		t.Fatal("restoration changed the original persisted transcript rows", readErr)
	}
	after, e := os.ReadFile(config)
	if e != nil || !bytes.Equal(after, beforeResume) {
		t.Fatal("restoration mutated old config/unknown fields", e, string(after))
	}
}

func TestPublicResumeRejectsUnmigratedNonemptySDKBetaProducer(t *testing.T) {
	args := append(mainRecipeArgs(), "--betas", "context-1m-2025-08-07")
	if _, e := ParsePrintArguments(args, "fixture"); e == nil || !strings.Contains(e.Error(), "normal print option business --betas") {
		t.Fatal("nonempty SDK beta input silently became nil metadata", e)
	}
}
