package filehistory_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/checkpointfiles"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/filehistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
)

func raw(v any) json.RawMessage { data, _ := json.Marshal(v); return data }
func object(data []byte) protocol.JSONObject {
	var out protocol.JSONObject
	_ = json.Unmarshal(data, &out)
	return out
}
func emptyState() protocol.JSONObject {
	return object([]byte(`{"snapshots":[],"trackedFiles":[],"snapshotSequence":0}`))
}
func setFields(fields map[string]any) protocol.JSONObject {
	out := emptyState()
	for k, v := range fields {
		out.Set(k, raw(v))
	}
	return out
}

type recordedSnapshot struct {
	ID       json.RawMessage     `json:"id"`
	Snapshot protocol.JSONObject `json:"snapshot"`
	Update   bool                `json:"update"`
}
type stateSink struct {
	owner   *cooperative.Owner
	binding *ownerasync.NativeBinding
	records []recordedSnapshot
	failure bool
}

func (s *stateSink) NativeCopyBinding() *ownerasync.NativeBinding { return s.binding }
func (s *stateSink) BeginFileHistorySnapshot(scope *cooperative.Scope, id json.RawMessage, snapshot *protocol.JSONObject, update bool) (*ownerasync.OwnerFuture[struct{}], error) {
	if !scope.BelongsTo(s.owner) {
		return nil, cooperative.ErrRestricted
	}
	s.records = append(s.records, recordedSnapshot{append(json.RawMessage(nil), id...), snapshot.Clone(), update})
	future, e := ownerasync.NewOwnerFuture[struct{}](s.owner)
	if e != nil {
		return nil, e
	}
	if s.failure {
		e = future.Reject(scope, errors.New("fixture record failed"))
	} else {
		e = future.Resolve(scope, struct{}{})
	}
	return future, e
}

type action map[string]any

func write(path, text string) action {
	return action{"op": "write", "path": path, "hex": fmt.Sprintf("%x", []byte(text))}
}
func operation(op, id string) action { return action{"op": op, "id": id} }
func track(path, id string) action   { return action{"op": "track", "id": id, "path": path} }
func oldSnapshot(id string, version int) map[string]any {
	return map[string]any{"messageId": id, "schemaVersion": version, "trackedFileBackups": map[string]any{}, "timestamp": "2020-01-01T00:00:00.000Z", "futureSnapshotField": map[string]any{"keep": true}}
}

type raceStateFiles struct {
	sessionrewind.NativeFiles
	slow             string
	started, release chan struct{}
}

func (f *raceStateFiles) ReadCurrentState(ctx context.Context, path string) (checkpointfiles.FileState, error) {
	if path == f.slow {
		close(f.started)
		<-f.release
	}
	return f.NativeFiles.ReadCurrentState(ctx, path)
}

func TestMakeTrackCompleteStateRecordAndDiskAgainstActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	cases := []struct {
		Name    string
		Initial map[string]any
		Actions []action
	}{
		{"repeat-version-delete-recreate", map[string]any{"futureStateField": map[string]any{"keep": true}}, []action{operation("make", "user-1"), write("a.txt", "before\n"), track("a.txt", "assistant-1"), write("a.txt", "after\n"), track("a.txt", "assistant-repeat"), operation("complete", "user-1"), write("a.txt", "later\n"), operation("complete", "user-1"), operation("make", "user-2"), operation("complete", "user-2"), {"op": "delete", "path": "a.txt"}, operation("make", "user-3"), write("a.txt", "recreated\n"), operation("make", "user-4"), operation("complete", "user-4")}},
		{"cap-and-sequence", map[string]any{"futureStateField": "keep"}, []action{{"op": "many-makes", "count": 104}}},
		{"old-schema", map[string]any{"snapshots": []any{oldSnapshot("old", 1)}}, []action{write("a.txt", "old source"), track("a.txt", "parent-old"), write("a.txt", "new source"), operation("complete", "old"), operation("complete", "old")}},
		{"future-schema", map[string]any{"snapshots": []any{oldSnapshot("future", 7)}, "snapshotSequence": 40}, []action{write("a.txt", "future source"), track("a.txt", "parent-future"), operation("complete", "future")}},
		{"partial-unsafe-and-inherit", nil, []action{operation("make", "p1"), write("a.txt", "safe before"), write("b.txt", "linked before"), track("a.txt", "pa"), track("b.txt", "pb"), {"op": "hardlink", "path": "b.txt", "alias": "b-alias"}, write("a.txt", "safe after"), operation("complete", "p1"), {"op": "unlink-alias", "alias": "b-alias"}, operation("complete", "p1"), {"op": "hardlink", "path": "b.txt", "alias": "b-alias"}, operation("make", "p2")}},
		{"utf8-equivalent-reuse", nil, []action{operation("make", "u1"), {"op": "write", "path": "a.txt", "hex": "ff"}, track("a.txt", "pu"), {"op": "write", "path": "a.txt", "hex": "fe"}, {"op": "mtime", "path": "a.txt", "time": 1900000000000.0}, operation("make", "u2")}},
		{"missing-snapshot-enabled-agent-noop", nil, []action{write("a.txt", "actual untouched"), track("a.txt", "missing"), {"op": "enabled", "enabled": false}, operation("make", "disabled"), {"op": "enabled", "enabled": true}, operation("make", "root"), {"op": "agent-noop", "id": "agent-fg", "path": "a.txt", "foreground": true}, {"op": "agent-noop", "id": "agent-bg", "path": "a.txt", "foreground": false}}},
		{"record-failures", nil, []action{{"op": "record-failure", "enabled": true}, operation("make", "r1"), write("a.txt", "record before"), track("a.txt", "pr"), write("a.txt", "record after"), operation("complete", "r1"), {"op": "record-failure", "enabled": false}, operation("complete", "r1")}},
		{"null-completion-and-sequence", map[string]any{"snapshots": []any{map[string]any{"messageId": "null-completion", "schemaVersion": nil, "completedFileBackups": nil, "trackedFileBackups": map[string]any{}, "timestamp": "2020-01-01T00:00:00.000Z", "futureNullField": nil}}, "snapshotSequence": nil}, []action{write("a.txt", "before null boundary"), track("a.txt", "pn"), operation("complete", "null-completion"), operation("make", "after-null")}},
		{"completed-empty-map-is-terminal", map[string]any{"snapshots": []any{map[string]any{"messageId": "empty-completed", "schemaVersion": 2, "completedFileBackups": map[string]any{}, "trackedFileBackups": map[string]any{}, "timestamp": "2020-01-01T00:00:00.000Z"}}}, []action{write("a.txt", "unchanged completion"), operation("complete", "empty-completed")}},
		{"null-backup-existing-key", map[string]any{"snapshots": []any{map[string]any{"messageId": "null-backup", "schemaVersion": 1, "trackedFileBackups": map[string]any{"a.txt": nil}, "timestamp": "2020-01-01T00:00:00.000Z"}}, "trackedFiles": []string{"a.txt"}}, []action{write("a.txt", "null prior value"), track("a.txt", "p-null"), operation("complete", "null-backup")}},
		{"parallel-two-changed", nil, []action{operation("make", "two-1"), write("a.txt", "first A"), write("b.txt", "first B"), track("a.txt", "ta"), track("b.txt", "tb"), write("a.txt", "changed A"), write("b.txt", "changed B"), operation("make", "two-2"), operation("complete", "two-2")}},
		{"fresh-inherit-during-make", nil, []action{operation("make", "concurrent-1"), write("a.txt", "first A"), track("a.txt", "track-A"), write("b.txt", "first B"), {"op": "race-make-track", "id": "concurrent-2", "slowPath": "a.txt", "newPath": "b.txt", "parentId": "track-B"}}},
		{"same-contents-reuse-and-mode-change", nil, []action{operation("make", "s1"), write("a.txt", "same"), track("a.txt", "ps"), write("a.txt", "same"), {"op": "mtime", "path": "a.txt", "time": 1900000000000.0}, operation("make", "s2"), {"op": "chmod", "path": "a.txt", "mode": 0600}, operation("make", "s3")}},
	}
	home := t.TempDir()
	temp := filepath.Join(home, "tmp")
	_ = os.MkdirAll(temp, 0700)
	_ = os.MkdirAll(filepath.Join(home, ".git"), 0700)
	rows := []map[string]any{}
	for _, row := range cases {
		rows = append(rows, map[string]any{"initial": row.Initial, "actions": row.Actions})
	}
	payload, _ := json.Marshal(map[string]any{"home": home, "cases": rows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-file-history-state.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 70*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FILE_HISTORY_STATE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=fake-file-history", "ANTHROPIC_BASE_URL=http://127.0.0.1:1"}
	cmd.Stdin = bytes.NewReader(payload)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatalf("Source fixture %v %.4000s", e, out)
	}
	type diskRow struct {
		Name, Hex string
		Mode      uint32
	}
	type step struct {
		State   protocol.JSONObject
		Records []recordedSnapshot
		Errors  []string
		Thrown  *struct{ Name, Message string }
		Disk    []diskRow
	}
	var receipt struct {
		Results []struct {
			Work, SessionID string
			Steps           []step
		}
		Capabilities struct{ Blocked []string }
	}
	if e = json.Unmarshal(out, &receipt); e != nil || len(receipt.Results) != len(cases) || len(receipt.Capabilities.Blocked) > 0 {
		t.Fatalf("Source shape %v %.4000s", e, out)
	}
	// UUIDs are independent entropy. Compare every filename's complete path hash,
	// version and boundary marker, replacing only the random nonce at the end.
	nonce := regexp.MustCompile(`(-completed-[^"/]*-)(?:[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|fixture-uuid-[0-9]+)`)
	equivalent := func(left, right any) bool {
		a, b := string(raw(left)), string(raw(right))
		a = nonce.ReplaceAllString(a, "${1}NONCE")
		b = nonce.ReplaceAllString(b, "${1}NONCE")
		var x, y any
		_ = json.Unmarshal([]byte(a), &x)
		_ = json.Unmarshal([]byte(b), &y)
		return reflect.DeepEqual(x, y)
	}
	for i, row := range cases {
		t.Run(row.Name, func(t *testing.T) {
			source := receipt.Results[i]
			owner := cooperative.New()
			defer owner.Close()
			var refs ownerasync.NativeReferences
			binding, _ := refs.Bind(owner)
			sink := &stateSink{owner: owner, binding: binding, records: []recordedSnapshot{}}
			config := filepath.Join(t.TempDir(), "config")
			files := sessionrewind.NativeFiles{ConfigHome: config}
			state := setFields(row.Initial)
			enabled := true
			now := time.UnixMilli(1700000000000)
			var uuid atomic.Int64
			notices := []string{}
			raceFiles := &raceStateFiles{NativeFiles: files}
			service, e := filehistory.New(filehistory.Options{Owner: owner, Binding: binding, Sink: sink, Files: raceFiles, ConfigHome: config, OriginalCWD: source.Work, SessionID: func(scope *cooperative.Scope) (string, error) {
				if !scope.BelongsTo(owner) {
					return "", cooperative.ErrRestricted
				}
				return source.SessionID, nil
			}, Enabled: func(*cooperative.Scope) (bool, error) { return enabled, nil }, GetState: func(*cooperative.Scope) (protocol.JSONObject, error) { return state.Clone(), nil }, SetState: func(_ *cooperative.Scope, value protocol.JSONObject) error { state = value.Clone(); return nil }, Now: func() time.Time { return now }, UUID: func() (string, error) { return fmt.Sprintf("fixture-uuid-%d", uuid.Add(1)), nil }, OnError: func(e error) { notices = append(notices, e.Error()) }})
			if e != nil {
				t.Fatal(e)
			}
			// Reset only this private Source work directory to its initial disk boundary.
			if e = os.RemoveAll(source.Work); e != nil {
				t.Fatal(e)
			}
			_ = os.MkdirAll(source.Work, 0700)
			for n, action := range row.Actions {
				recordStart, errorStart := len(sink.records), len(notices)
				var returned error
				e = owner.Run(ctx, func(scope *cooperative.Scope) error {
					if v, ok := action["now"].(float64); ok {
						now = time.UnixMilli(int64(v))
					} else {
						now = time.UnixMilli(1700000000000)
					}
					path := filepath.Join(source.Work, fmt.Sprint(action["path"]))
					switch action["op"] {
					case "write":
						var data []byte
						_, _ = fmt.Sscanf(fmt.Sprint(action["hex"]), "%x", &data)
						mode := os.FileMode(0640)
						if m, ok := action["mode"].(int); ok {
							mode = os.FileMode(m)
						}
						returned = os.WriteFile(path, data, mode)
					case "chmod":
						returned = os.Chmod(path, os.FileMode(action["mode"].(int)))
					case "mtime":
						stamp := time.UnixMilli(int64(action["time"].(float64)))
						returned = os.Chtimes(path, stamp, stamp)
					case "delete":
						returned = os.Remove(path)
					case "hardlink":
						returned = os.Link(path, filepath.Join(source.Work, fmt.Sprint(action["alias"])))
					case "unlink-alias":
						returned = os.Remove(filepath.Join(source.Work, fmt.Sprint(action["alias"])))
					case "enabled":
						enabled = action["enabled"].(bool)
					case "record-failure":
						sink.failure = action["enabled"].(bool)
					case "make":
						returned = service.Make(scope, raw(action["id"]))
					case "many-makes":
						for j := 0; j < action["count"].(int); j++ {
							if returned = service.Make(scope, raw(fmt.Sprintf("bulk-%d", j))); returned != nil {
								break
							}
						}
					case "track":
						returned = service.Track(scope, path, raw(action["id"]))
					case "complete":
						returned = service.Complete(scope, raw(action["id"]))
					case "race-make-track":
						raceFiles.slow = filepath.Join(source.Work, fmt.Sprint(action["slowPath"]))
						raceFiles.started = make(chan struct{})
						raceFiles.release = make(chan struct{})
						go func() {
							<-raceFiles.started
							receipt, e := owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
								return service.Track(next, filepath.Join(source.Work, fmt.Sprint(action["newPath"])), raw(action["parentId"]))
							})
							if e == nil {
								e = receipt.Wait()
							}
							close(raceFiles.release)
						}()
						returned = service.Make(scope, raw(action["id"]))
						raceFiles.slow = ""

					case "agent-noop": // Source createSubagentContext supplies a no-op updater to all three functions; no service call may mutate main state.
					default:
						return fmt.Errorf("unknown action %v", action)
					}
					return scope.AwaitFulfilled()
				})
				if e != nil {
					t.Fatal(e)
				}
				wanted := source.Steps[n]
				if !equivalent(state, wanted.State) {
					t.Errorf("step%d %v state Go%s Source%s", n, action["op"], raw(state), raw(wanted.State))
				}
				if !equivalent(sink.records[recordStart:], wanted.Records) {
					t.Errorf("step%d %v record Go%s Source%s", n, action["op"], raw(sink.records[recordStart:]), raw(wanted.Records))
				}
				if (returned != nil) != (wanted.Thrown != nil) {
					t.Errorf("step%d thrown Go%v Source%+v", n, returned, wanted.Thrown)
				}
				if len(notices[errorStart:]) != len(wanted.Errors) {
					t.Errorf("step%d notices Go%v Source%v", n, notices[errorStart:], wanted.Errors)
				}
				disk := []diskRow{}
				directory := filepath.Join(config, "file-history", source.SessionID)
				entries, e := os.ReadDir(directory)
				if e != nil && !os.IsNotExist(e) {
					t.Fatal(e)
				}
				for _, entry := range entries {
					data, e := os.ReadFile(filepath.Join(directory, entry.Name()))
					if e != nil {
						t.Fatal(e)
					}
					info, _ := entry.Info()
					disk = append(disk, diskRow{entry.Name(), fmt.Sprintf("%x", data), uint32(info.Mode().Perm())})
				}
				if !equivalent(disk, wanted.Disk) {
					t.Errorf("step%d disk Go%s Source%s", n, raw(disk), raw(wanted.Disk))
				}
			}
			if count, _ := refs.Snapshot(); count != 0 {
				t.Errorf("native jobs survived completed physical steps: %d", count)
			}
		})
	}
}
