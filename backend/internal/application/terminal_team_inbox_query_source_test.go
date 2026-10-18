package application

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceInteractiveInboxIdleNotificationFocusedGatesAndQueryPOST(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("original pinned Source Bun required")
	}
	home := t.TempDir()
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-inbox-query.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "PATH=/usr/bin:/bin", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_TEAM_INBOX_QUERY_SOURCE=1", "CC_HAHA_AGENT_TEAMS_ENABLED=1", "CLAUDE_CODE_ENABLE_TASKS=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "NODE_ENV=production", "USER_TYPE=external", "BUN_OPTIONS=--no-env-file"}
	command.Stdin = bytes.NewReader(raw(map[string]any{"home": home, "modes": []string{"idle", "teams", "permission", "busy", "human-queue", "approval-trusted", "approval-forged", "approval-unknown-member", "approval-unknown-field", "approval-duplicate"}}))
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if err != nil {
		t.Fatalf("actual original inbox consumer %v stderr=%s output=%.12000s", err, stderr.String(), output)
	}
	var source struct {
		Rows []struct {
			Mode   string
			Before struct {
				Focus   *string
				Queries int
				Pending []string
			}
			After struct {
				Queries int
				Pending int
			}
			Bodies    []json.RawMessage
			Admitted  []json.RawMessage
			Errors    []string
			Envelopes []struct {
				From, Text string
				Body       protocol.JSONObject
			}
			TeamFile              protocol.JSONObject
			Roster                protocol.JSONObject
			Tasks                 []protocol.JSONObject
			DuplicateAfterRemoval *struct{ NewQueries, Pending int }
		}
		Blocked []string
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != 10 || len(source.Blocked) != 0 || stderr.Len() != 0 {
		t.Fatalf("source projection %v stderr=%s output=%.12000s", err, stderr.String(), output)
	}
	for _, row := range source.Rows {
		t.Run(row.Mode, func(t *testing.T) {
			expected := 1
			if row.Mode == "human-queue" {
				expected = 0
			}
			if strings.HasPrefix(row.Mode, "approval-") {
				if len(row.Errors) != 0 || row.After.Queries != len(row.Bodies) || len(row.Admitted) != len(row.Bodies) || row.After.Pending != 0 {
					t.Fatalf("actual original approval query/queue completion: %+v", row)
				}
				trusted := row.Mode == "approval-trusted" || row.Mode == "approval-duplicate"
				if len(row.Tasks) != 1 {
					t.Fatal("actual private task producer missing", row.Tasks)
				}
				task := row.Tasks[0]
				if trusted {
					if row.Roster.Get("worker@inbox-query-"+row.Mode) != nil || task.Get("owner") != nil || task.String("status") != "pending" {
						t.Fatalf("original roster/task ownership not released: %s %s", raw(row.Roster), raw(task))
					}
					members := array(row.TeamFile.Get("members"))
					if len(members) != 1 || members[0].String("name") != "team-lead" {
						t.Fatalf("original physical roster remove: %s", raw(row.TeamFile))
					}
					approvals, terminations := 0, 0
					for _, envelope := range row.Envelopes {
						switch envelope.Body.String("type") {
						case "shutdown_approved":
							approvals++
							if envelope.From != "worker" || envelope.Body.String("requestId") != "actual-private-shutdown" {
								t.Fatalf("invalid approval entered actual POST: %+v", envelope)
							}
						case "teammate_terminated":
							terminations++
							if envelope.From != "system" || !strings.Contains(envelope.Body.String("message"), "worker has shut down.") || !strings.Contains(envelope.Body.String("message"), `1 task(s) were unassigned: #1 "REAL_ASSIGNED_SHUTDOWN_TASK"`) {
								t.Fatalf("original TaskUnassign notification missing actual POST: %+v", envelope)
							}
						default:
							t.Fatalf("unexpected original notification: %+v", envelope)
						}
					}
					expectedApprovals := 1
					if row.Mode == "approval-duplicate" {
						expectedApprovals = 2
					}
					if approvals != expectedApprovals || terminations != 1 || len(row.Envelopes) != approvals+1 {
						t.Fatalf("actual original approval exact contents approval=%d termination=%d %+v", approvals, terminations, row.Envelopes)
					}
					if row.Mode == "approval-duplicate" && (row.DuplicateAfterRemoval == nil || row.DuplicateAfterRemoval.NewQueries != 0 || row.DuplicateAfterRemoval.Pending != 0) {
						t.Fatalf("duplicate after physical roster removal was delivered again: %+v", row.DuplicateAfterRemoval)
					}
				} else {
					if row.Roster.Get("worker@inbox-query-"+row.Mode) == nil || task.String("owner") != "worker@inbox-query-"+row.Mode || task.String("status") != "in_progress" || len(array(row.TeamFile.Get("members"))) != 2 {
						t.Fatalf("untrusted approval altered actual business ownership: %s %s %s", raw(row.Roster), raw(task), raw(row.TeamFile))
					}
					if row.Mode == "approval-unknown-field" {
						if len(row.Envelopes) != 1 || string(row.Envelopes[0].Body.Get("future")) != "true" || row.After.Queries != 1 {
							t.Fatalf("strict-invalid schema must remain ordinary chat, with no cleanup: %+v", row)
						}
					} else if row.After.Queries != 0 || len(row.Envelopes) != 0 {
						t.Fatalf("untrusted sender/member became original query: %+v", row)
					}
				}
				return
			}
			if len(row.Errors) != 0 || row.After.Queries != expected || len(row.Bodies) != expected {
				t.Fatalf("actual original query counts errors=%v queries=%d bodies=%d", row.Errors, row.After.Queries, len(row.Bodies))
			}
			if row.Mode == "permission" && (row.Before.Focus == nil || *row.Before.Focus != "tool-permission" || row.Before.Queries != 0 || len(row.Before.Pending) != 1) {
				t.Fatalf("true focused permission=%+v", row.Before)
			}
			if row.Mode == "teams" && row.Before.Focus != nil {
				t.Fatal("original Teams local JSX invented a focused-input gate", *row.Before.Focus)
			}
			if (row.Mode == "busy" || row.Mode == "human-queue") && (row.Before.Queries != 0 || len(row.Before.Pending) != 1) {
				t.Fatalf("actual busy/human queue admission=%+v", row.Before)
			}
			if expected == 1 && (!strings.Contains(string(row.Bodies[0]), "idle_notification") || !strings.Contains(string(row.Bodies[0]), "teammate-message") || len(row.Admitted) != 1) {
				t.Fatalf("original idle notification missing from physical next POST: %s", row.Bodies)
			}
		})
	}
	t.Logf("Original mounted InboxPoller + whole REPL focus/admission callbacks + real QueryEngine POST: %s", output)
}
