package server

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamworker"
)

// This factory observes launch mode only. The application physical test uses
// the actual native worker/SDK factory and validates cold startup and Stop.
type resumeSelectionWorker struct{ transportSession }

func (*resumeSelectionWorker) AcknowledgeTeamWorkerModel(context.Context, string) (protocol.JSONObject, error) {
	return protocol.NewJSONObject(), nil
}
func (*resumeSelectionWorker) SubmitTeamTask(context.Context, protocol.JSONObject) error { return nil }

func TestTeamWorkerResumeLaunchModeMatchesActualSourceFileDiscovery(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	home := t.TempDir()
	for _, name := range []string{".claude", "tmp"} {
		if err := os.MkdirAll(filepath.Join(home, name), 0700); err != nil {
			t.Fatal(err)
		}
	}
	valid := `{"type":"user","uuid":"old","parentUuid":null,"message":{"role":"user","content":"original saved worker"}}` + "\n"
	rows := []map[string]any{{"requested": true, "contents": nil}, {"requested": true, "contents": valid}, {"requested": true, "contents": ""}, {"requested": false, "contents": valid}, {"requested": false, "contents": nil}}
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-worker-resume-start.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_WORKER_RESUME_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": rows})
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows         []struct{ ShouldResume bool }
		Capabilities struct{ Blocked []string }
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) || len(source.Capabilities.Blocked) != 0 {
		t.Fatal(err, string(output))
	}
	for i, row := range rows {
		t.Run(fmt.Sprintf("profile-%d", i), func(t *testing.T) {
			launches := make(chan SessionOptions, 1)
			options := testOptions(t, func(_ context.Context, o SessionOptions, _ Emit) (LiveSession, error) {
				launches <- o
				return &resumeSelectionWorker{}, nil
			})
			s, _ := testHost(t, options)
			id := fmt.Sprintf("11111111-2222-4333-8444-%012d", i)
			if contents, ok := row["contents"].(string); ok {
				directory := filepath.Join(options.ConfigDir, "projects", "original-worker")
				if err := os.MkdirAll(directory, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(directory, id+".jsonl"), []byte(contents), 0600); err != nil {
					t.Fatal(err)
				}
			}
			parentCtx, parentCancel := context.WithCancel(s.ctx)
			defer parentCancel()
			s.mu.Lock()
			s.runtimes["parent"] = &runtimeSession{ctx: parentCtx, cancel: parentCancel, live: &transportSession{}}
			s.mu.Unlock()
			if _, err := s.PrepareTeamWorker(ctx, TeamWorkerLaunch{ID: id, Model: "fixture-model", WorkDir: options.WorkDir, Resume: row["requested"].(bool), Worker: teamworker.Start{ParentSessionID: "parent", MemberID: "worker", AgentDefinition: protocol.NewJSONObject()}}); err != nil {
				t.Fatal(err)
			}
			select {
			case actual := <-launches:
				if actual.Resume != source.Rows[i].ShouldResume || actual.ID != id {
					t.Fatal("actual factory launch mode diverged from original file discovery", actual.Resume, source.Rows[i].ShouldResume, actual.ID)
				}
			case <-ctx.Done():
				t.Fatal("worker factory was not reached")
			}
			if err := s.StopTeamWorker(ctx, id); err != nil {
				t.Fatal(err)
			}
		})
	}
}
