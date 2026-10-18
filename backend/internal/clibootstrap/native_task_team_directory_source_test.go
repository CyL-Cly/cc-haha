package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/teamstore"
)

type nativeTaskTeamDirectorySource struct {
	Initial, Next, List, Team, Member string
	First, Second                     struct{ TaskID string }
	OldPaths                          struct{ Tasks, Inbox, Config string }
	Before, After                     struct {
		Task, Config json.RawMessage
		Inbox        []json.RawMessage
		History      []json.RawMessage
	}
	BeforeFiles, AfterFiles map[string]map[string]string
	Caps                    struct{ Blocked, SecureReads []string }
}

func actualTaskTeamDirectorySource(t *testing.T, home, profile string) nativeTaskTeamDirectorySource {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Pinned actual task/team current-directory Source required")
	}
	fixture, err := filepath.Abs("../../../scripts/quality-gate/go-parity/native-config-dir-task-team.fixture.ts")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--feature=TRANSCRIPT_CLASSIFIER", "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CONFIG_DIR_TASK_TEAM_SOURCE=1", "NODE_ENV=production", "USER_TYPE=external", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	command.Stdin = bytes.NewReader(rawMain(map[string]string{"home": home, "profile": profile}))
	output, err := command.CombinedOutput()
	var source nativeTaskTeamDirectorySource
	if err != nil || json.Unmarshal(output, &source) != nil || source.BeforeFiles == nil || len(source.Caps.Blocked) != 0 || len(source.Caps.SecureReads) != 0 {
		t.Fatalf("whole physical task/team Source %v %.5000s", err, output)
	}
	return source
}

func TestActualNativeRootTaskAndTeamCurrentDirectoryAgainstWholeSource(t *testing.T) {
	var source nativeTaskTeamDirectorySource
	m := backgroundAgentMainWithOptions(t, "http://127.0.0.1:1", func(n *NativeBoundary) {
		source = actualTaskTeamDirectorySource(t, n.Home, "between")
		restoreNativeTaskTeamSourceFiles(t, source)
		n.Environment["CC_HAHA_AGENT_TEAMS_ENABLED"] = "1"
	}, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := m.Owner.Run(ctx, func(scope *cooperative.Scope) error {
		if _, err := m.nativeTeamBindings(scope); err != nil {
			return err
		}
		config := m.teamLeader.config
		mailbox, err := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: source.Initial, TaskStore: m.taskStore})
		if err != nil {
			return err
		}
		taskStore := m.taskStore
		beforeTask, err := taskStore.Get(source.List, quoteMain(source.First.TaskID))
		if err != nil {
			return err
		}
		if beforeTask == nil {
			return errors.New("actual root task seed was not consumed before changing directory")
		}
		before := map[string]any{"task": beforeTask, "config": config.ReadAsync(source.Team), "inbox": json.RawMessage(mailbox.Read(source.Member, source.Team))}
		assertNativeTaskTeamJSON(t, "warm old directory", before, map[string]any{"task": source.Before.Task, "config": source.Before.Config, "inbox": source.Before.Inbox})
		if err := m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Next)); err != nil {
			return err
		}
		if _, err := taskStore.UpdateTask(source.List, quoteMain(source.Second.TaskID), protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: quoteMain("new-updated")})); err != nil {
			return err
		}
		if _, err := config.Mutate(source.Team, func(value protocol.JSONObject) (protocol.JSONObject, error) {
			value.Set("description", quoteMain("new-updated"))
			value.Set("futureMutation", []byte(`{"keep":true}`))
			return value, nil
		}); err != nil {
			return err
		}
		unread, err := mailbox.Unread(source.Member, source.Team)
		if err != nil {
			return err
		}
		if _, err := mailbox.Claim(source.Member, source.Team, unread); err != nil {
			return err
		}
		message := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain("new-after-switch")}, protocol.JSONField{Name: "from", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "text", Value: quoteMain("new-after-switch")}, protocol.JSONField{Name: "timestamp", Value: quoteMain("2026-10-14T00:00:00.000Z")})
		if err := mailbox.Write(source.Member, source.Team, message); err != nil {
			return err
		}
		afterTask, err := taskStore.Get(source.List, quoteMain(source.Second.TaskID))
		if err != nil {
			return err
		}
		after := map[string]any{"task": afterTask, "config": config.ReadAsync(source.Team), "inbox": json.RawMessage(mailbox.Read(source.Member, source.Team)), "history": json.RawMessage(mailbox.ReadHistory(source.Member, source.Team))}
		assertNativeTaskTeamJSON(t, "same-root current directory", after, map[string]any{"task": source.After.Task, "config": source.After.Config, "inbox": source.After.Inbox, "history": source.After.History})
		assertNativeTaskTeamSourceFiles(t, source)
		if m.taskStore != taskStore || m.teamLeader.config != config {
			t.Error("directory reader replaced root singleton objects")
		}
		if _, err := m.Startup.GetInitialSettings(); err == nil || !strings.Contains(err.Error(), "changed filesystem/schema host CLAUDE_CONFIG_DIR") {
			t.Error("ordinary dynamic directory guard changed", err)
		}
		return m.environment.SetProperty(quoteMain("CLAUDE_CONFIG_DIR"), quoteMain(source.Initial))
	}); err != nil {
		t.Fatal(err)
	}
}

func restoreNativeTaskTeamSourceFiles(t *testing.T, source nativeTaskTeamDirectorySource) {
	t.Helper()
	for label, files := range source.BeforeFiles {
		directory := source.Initial
		if label == "next" {
			directory = source.Next
		}
		// The original Source run has already written its expected outcome.
		// Restore the exact pre-operation tree, including absence of history
		// and newly-created tasks, before any Go consumer is constructed.
		if err := os.RemoveAll(directory); err != nil {
			t.Fatal(err)
		}
		for name, value := range files {
			path := filepath.Join(directory, name)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(value), 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func assertNativeTaskTeamSourceFiles(t *testing.T, source nativeTaskTeamDirectorySource) {
	t.Helper()
	for label, files := range source.AfterFiles {
		expected := map[string]string{}
		for name, value := range files {
			if strings.HasPrefix(name, "tasks/") || strings.HasPrefix(name, "teams/") {
				expected[name] = value
			}
		}
		directory := source.Initial
		if label == "next" {
			directory = source.Next
		}
		actual := map[string]string{}
		err := filepath.WalkDir(directory, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() {
				if filepath.Dir(path) == directory && entry.Name() != "tasks" && entry.Name() != "teams" {
					return filepath.SkipDir
				}
				if strings.HasSuffix(entry.Name(), ".lock") {
					return errors.New("actual task/team consumer retained a physical lease")
				}
				return nil
			}
			name, err := filepath.Rel(directory, path)
			if err != nil {
				return err
			}
			if !strings.HasPrefix(name, "tasks/") && !strings.HasPrefix(name, "teams/") {
				return nil
			}
			value, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			actual[name] = string(value)
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(actual) != len(expected) {
			t.Errorf("%s physical task/team tree differs: Go paths %v Source paths %v", label, reflect.ValueOf(actual).MapKeys(), reflect.ValueOf(expected).MapKeys())
		}
		for name, want := range expected {
			got, present := actual[name]
			if !present {
				t.Errorf("%s missing original physical file %s", label, name)
				continue
			}
			if json.Valid([]byte(want)) {
				assertNativeTaskTeamJSON(t, label+"/"+name, json.RawMessage(got), json.RawMessage(want))
			} else if strings.HasSuffix(name, ".history.jsonl") {
				gotRows, wantRows := strings.FieldsFunc(got, func(r rune) bool { return r == '\n' }), strings.FieldsFunc(want, func(r rune) bool { return r == '\n' })
				if len(gotRows) != len(wantRows) {
					t.Errorf("%s history length Go %d Source %d", name, len(gotRows), len(wantRows))
					continue
				}
				for i := range wantRows {
					assertNativeTaskTeamJSON(t, name, json.RawMessage(gotRows[i]), json.RawMessage(wantRows[i]))
				}
			} else if got != want {
				t.Errorf("%s physical content differs Go %q Source %q", name, got, want)
			}
		}
	}
}

func TestActualTaskTeamCapturedLeasePathsAgainstWholeSource(t *testing.T) {
	for _, profile := range []string{"held-lifecycle", "held-mailbox", "held-config"} {
		t.Run(profile, func(t *testing.T) {
			home := filepath.Join(t.TempDir(), "home")
			if err := os.MkdirAll(home, 0700); err != nil {
				t.Fatal(err)
			}
			source := actualTaskTeamDirectorySource(t, home, profile)
			restoreNativeTaskTeamSourceFiles(t, source)
			current := source.Initial
			// The sampler is an input-only observer of the same current
			// directory getter. Leases and every body below are the real
			// taskstore/teamstore implementations, including mkdir locking.
			var directoryMu sync.Mutex
			samples := make(chan string, 64)
			setDirectory := func(value string) { directoryMu.Lock(); current = value; directoryMu.Unlock() }
			store, err := taskstore.New(taskstore.Options{ConfigDir: source.Initial, PID: os.Getpid(), Now: time.Now, NativeConfigDirectory: func() string {
				directoryMu.Lock()
				value := current
				directoryMu.Unlock()
				samples <- value
				return value
			}})
			if err != nil {
				t.Fatal(err)
			}
			message := protocol.NewJSONObject(protocol.JSONField{Name: "id", Value: quoteMain("after-held-mailbox")}, protocol.JSONField{Name: "from", Value: quoteMain("team-lead")}, protocol.JSONField{Name: "text", Value: quoteMain("after-held-mailbox")}, protocol.JSONField{Name: "timestamp", Value: quoteMain("2026-10-14T00:00:00.000Z")})
			if profile == "held-lifecycle" {
				err = store.WithTeamLifecycleTransaction(source.List, func(scoped *taskstore.Service) error {
					lease := filepath.Join(source.Initial, "tasks", ".lifecycle-locks", taskstore.SanitizePathComponent(source.List)+".lock.lock")
					if info, err := os.Stat(lease); err != nil || !info.IsDir() {
						return errors.New("original old lifecycle lease was not physically held")
					}
					setDirectory(source.Next)
					_, err := scoped.Create(source.List, protocol.NewJSONObject(protocol.JSONField{Name: "subject", Value: quoteMain("after-held-lifecycle")}, protocol.JSONField{Name: "description", Value: quoteMain("after-held-lifecycle description")}, protocol.JSONField{Name: "status", Value: quoteMain("pending")}, protocol.JSONField{Name: "blocks", Value: []byte("[]")}, protocol.JSONField{Name: "blockedBy", Value: []byte("[]")}, protocol.JSONField{Name: "metadata", Value: []byte(`{"future":{"keep":"after-held-lifecycle"}}`)}))
					return err
				})
			} else {
				var release func() error
				var work func() error
				sampleCount := 1
				if profile == "held-mailbox" {
					release, err = store.AcquireMailboxLease(source.OldPaths.Inbox)
					mailbox, construction := teamstore.NewMailbox(teamstore.MailboxOptions{ConfigDir: source.Initial, TaskStore: store})
					if construction != nil {
						t.Fatal(construction)
					}
					work = func() error { return mailbox.Write(source.Member, source.Team, message) }
				} else {
					sampleCount = 2 // access directory, then capture config lock target
					release, err = store.AcquireTeamConfigLease(source.OldPaths.Config)
					config, construction := teamstore.NewConfig(teamstore.ConfigOptions{ConfigDir: source.Initial, TaskStore: store, PID: os.Getpid()})
					if construction != nil {
						t.Fatal(construction)
					}
					work = func() error {
						_, err := config.Mutate(source.Team, func(value protocol.JSONObject) (protocol.JSONObject, error) {
							value.Set("description", quoteMain("after-held-config"))
							value.Set("futureMutation", []byte(`{"keep":true}`))
							return value, nil
						})
						return err
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				done := make(chan error, 1)
				go func() { done <- work() }()
				for i := 0; i < sampleCount; i++ {
					select {
					case value := <-samples:
						if value != source.Initial {
							t.Errorf("admitted path sampled %q before switch", value)
						}
					case <-time.After(time.Second):
						_ = release()
						t.Fatal("original operation did not capture its old lock target")
					}
				}
				setDirectory(source.Next)
				if err := release(); err != nil {
					t.Fatal(err)
				}
				select {
				case err = <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("original admitted operation did not settle after physical release")
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			assertNativeTaskTeamSourceFiles(t, source)
		})
	}
}

func assertNativeTaskTeamJSON(t *testing.T, label string, actual, source any) {
	t.Helper()
	var got, want any
	actualRaw, sourceRaw := rawMain(actual), rawMain(source)
	if json.Unmarshal(actualRaw, &got) != nil || json.Unmarshal(sourceRaw, &want) != nil || !reflect.DeepEqual(got, want) {
		t.Errorf("%s differs from original consumer\nGo %s\nSource %s", label, actualRaw, sourceRaw)
	}
}
