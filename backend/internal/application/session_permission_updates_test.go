package application

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestSessionAlwaysPermissionNormalizationAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned Source Bun unavailable")
	}
	rows := []json.RawMessage{
		[]byte(`{"toolName":"Write"}`),
		[]byte(`{"suggestions":[],"toolName":"WebFetch"}`),
		[]byte(`{"suggestions":null,"toolName":"Read"}`),
		[]byte(`{"suggestions":{},"toolName":"Read"}`),
		[]byte(`{"suggestions":[{"type":"addRules","rules":[{"toolName":"WebFetch","ruleContent":"domain:fixture.invalid"}],"behavior":"allow","destination":"localSettings"}],"toolName":"WebFetch"}`),
		[]byte(`{"suggestions":[{"destination":"userSettings","unknown":"\ud800","type":"setMode","mode":"acceptEdits"},{"type":"addDirectories","directories":["/fixture"],"destination":"projectSettings"}],"toolName":"ExitPlanMode"}`),
		[]byte(`{"suggestions":[null,false,3,"x",[],["\ud800"],{"0":"x","destination":"userSettings"}],"toolName":"Write"}`),
	}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-session-permission.fixture.ts")
	home := t.TempDir()
	data, _ := json.Marshal(rows)
	cmd := exec.Command(bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "BUN_OPTIONS=--no-env-file", "CC_HAHA_SESSION_PERMISSION_FIXTURE=1", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(data)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatal(err, string(out))
	}
	var expected []json.RawMessage
	if err := json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err, string(out))
	}
	if len(expected) != len(rows) {
		t.Fatal("Source rows missing", len(expected))
	}
	for i, row := range rows {
		input := obj(row)
		got := normalizeSessionPermissionUpdates(input.Get("suggestions"), input.String("toolName"))
		a, _ := protocol.ParseAndStringifyJavaScriptJSON(got)
		b, _ := protocol.ParseAndStringifyJavaScriptJSON(expected[i])
		if string(a) != string(b) {
			t.Fatalf("row %d Source=%s Go=%s", i, b, a)
		}
	}
}

func TestAlwaysAllowUsesActualSessionPermissionsAndNextHumanTurn(t *testing.T) {
	for _, always := range []bool{false, true} {
		t.Run(fmt.Sprintf("always=%v", always), func(t *testing.T) {
			var posts atomic.Int32
			provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				n := posts.Add(1)
				if n == 1 || n == 3 {
					response(w, fmt.Sprintf("write-%d", n), "Write", map[string]any{"file_path": fmt.Sprintf("approved-%d.txt", n), "content": "SESSION_APPROVAL_OK"}, "")
				} else {
					response(w, fmt.Sprintf("done-%d", n), "", nil, "SESSION_APPROVAL_OK")
				}
			}))
			defer provider.Close()
			o := nativeSessionOptions(t, provider.URL)
			o.Arguments.Tools = []string{"Write"}
			o.Arguments.PermissionMode = "default"
			s, err := NewSession(context.Background(), o)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close(context.Background())
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			asks := 0
			responses := make(chan error, 2)
			for turn := 0; turn < 2; turn++ {
				err = s.Submit(ctx, tui.Input{Text: "Write the approved marker."}, func(e tui.Event) error {
					if e.Kind == tui.PermissionAsked {
						asks++
						decision := tui.PermissionDecision{Allowed: true}
						if always {
							decision.Rule = []byte(` "alw\u0061ys" `)
						}
						id := e.Permission.ID
						go func() { responses <- s.RespondPermission(ctx, id, decision) }()
					}
					return nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			wantAsks := 2
			if always {
				wantAsks = 1
			}
			if asks != wantAsks || posts.Load() != 4 {
				t.Fatal("approval/context lifetime changed", asks, posts.Load())
			}
			for i := 0; i < asks; i++ {
				if err := <-responses; err != nil {
					t.Fatal(err)
				}
			}
			for _, n := range []int{1, 3} {
				data, err := os.ReadFile(filepath.Join(o.WorkDir, fmt.Sprintf("approved-%d.txt", n)))
				if err != nil || string(data) != "SESSION_APPROVAL_OK" {
					t.Fatal("actual Write did not execute", string(data), err)
				}
			}
			if _, err := os.Stat(filepath.Join(o.Environment["CLAUDE_CONFIG_DIR"], "settings.json")); !os.IsNotExist(err) {
				t.Fatal("session approval wrote shared settings", err)
			}
		})
	}
}
