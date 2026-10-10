package builtintools

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/taskstore"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type taskCorpus struct {
	Observations []struct {
		Step        struct{ Name, Tool, Operation, InputSource, AgentID, TaskListOverride, Path string }
		Observation struct {
			Success                                                                bool
			TaskListID, DataSource, ResultSource, MappedSource, StateSource, Error string
			Files                                                                  map[string]string
			Updates                                                                int64
		}
	}
	SourceOnly struct{ Capabilities struct{ Blocked []string } }
}

func sourceTaskCorpus(t *testing.T) taskCorpus {
	t.Helper()
	data, e := os.ReadFile("../taskstore/testdata/native_tasks_contract.json")
	if e != nil {
		t.Fatal(e)
	}
	var corpus taskCorpus
	if e = json.Unmarshal(data, &corpus); e != nil {
		t.Fatal(e)
	}
	if len(corpus.Observations) != 39 || len(corpus.SourceOnly.Capabilities.Blocked) > 0 {
		t.Fatal("missing actual Source task capture")
	}
	return corpus
}
func TestActualTaskToolsFilesResultsAndStateMatch39StepSource(t *testing.T) {
	corpus := sourceTaskCorpus(t)
	s, owner, home, _ := fixture(t)
	config := filepath.Join(home, ".claude")
	now := time.Now()
	var updates atomic.Int64
	list := ""
	expanded := "none"
	store, e := taskstore.New(taskstore.Options{ConfigDir: config, PID: os.Getpid(), Now: func() time.Time { return now }, OnUpdate: func() { updates.Add(1) }})
	if e != nil {
		t.Fatal(e)
	}
	s.options.TaskStore = store
	s.options.NonInteractive = false
	s.options.TaskListID = func(scope *cooperative.Scope, agentID string) (string, error) {
		if !scope.BelongsTo(owner) {
			return "", cooperative.ErrRestricted
		}
		return list, nil
	}
	s.options.ExpandTaskView = func(scope *cooperative.Scope) error {
		if !scope.BelongsTo(owner) {
			return cooperative.ErrRestricted
		}
		expanded = "tasks"
		return nil
	}
	s.options.TaskHook = func(context.Context, *cooperative.Scope, string, protocol.JSONObject) error { return nil }
	for _, row := range corpus.Observations {
		if row.Step.Operation == "parse" {
			continue
		}
		t.Run(row.Step.Name, func(t *testing.T) {
			list = row.Observation.TaskListID
			if row.Step.Operation == "seed" {
				path := filepath.Join(config, "tasks", row.Step.Path)
				if e := os.MkdirAll(filepath.Dir(path), 0777); e != nil {
					t.Fatal(e)
				}
				if e := os.WriteFile(path, []byte(row.Step.InputSource), 0666); e != nil {
					t.Fatal(e)
				}
			} else {
				if row.Observation.ResultSource != "" {
					result := input(row.Observation.ResultSource)
					stamp := result.String("taskListMutationAt")
					if stamp == "" {
						stamp = result.String("taskListSnapshotAt")
					}
					if stamp != "" {
						now, e = time.Parse(time.RFC3339Nano, stamp)
						if e != nil {
							t.Fatal(e)
						}
					}
				}
				var result queryloop.ToolCallResult
				e = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					definition, e := toolschema.NativeBuiltinDefinition(row.Step.Tool)
					if e != nil {
						return e
					}
					parsed, e := toolschema.ParseInputJSON(definition, []byte(row.Step.InputSource))
					if e != nil {
						return e
					}
					decision, e := s.Permission(scope, row.Step.Tool, parsed)
					if e != nil {
						return e
					}
					if decision != "allow" {
						t.Fatalf("Source task intrinsic permission %s", decision)
					}
					future, e := s.startTask(context.Background(), row.Step.Tool, parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope, AgentID: row.Step.AgentID}})
					if e != nil {
						return e
					}
					result, e = future.Await(scope)
					return e
				})
				if row.Observation.Error != "" {
					if e == nil || e.Error() != row.Observation.Error {
						t.Fatalf("error %v want %s", e, row.Observation.Error)
					}
				} else {
					if e != nil {
						t.Fatal(e)
					}
					if !jsonSame(result.Data, []byte(row.Observation.ResultSource)) {
						t.Fatalf("result %s want %s", result.Data, row.Observation.ResultSource)
					}
					mapped, e := MapResult(row.Step.Tool, result.Data, quote("actual-task-use-id"))
					if e != nil {
						t.Fatal(e)
					}
					if !jsonSame(raw(mapped), []byte(row.Observation.MappedSource)) {
						t.Fatalf("mapped %s want %s", raw(mapped), row.Observation.MappedSource)
					}
				}
				state := object(field("todos", map[string]any{}), field("expandedView", expanded))
				if !jsonSame(state, []byte(row.Observation.StateSource)) {
					t.Fatalf("state %s want %s", state, row.Observation.StateSource)
				}
			}
			files := map[string]string{}
			directory := filepath.Join(config, "tasks", taskstore.SanitizePathComponent(list))
			entries, e := os.ReadDir(directory)
			if e != nil && !errors.Is(e, os.ErrNotExist) {
				t.Fatal(e)
			}
			for _, entry := range entries {
				if strings.HasSuffix(entry.Name(), ".lock") {
					continue
				}
				data, e := os.ReadFile(filepath.Join(directory, entry.Name()))
				if e != nil {
					t.Fatal(e)
				}
				files[entry.Name()] = string(data)
			}
			if len(files) != len(row.Observation.Files) {
				t.Fatalf("files %#v want %#v", files, row.Observation.Files)
			}
			for name, want := range row.Observation.Files {
				if files[name] != want {
					t.Fatalf("physical %s changed:\n%s\nwant:\n%s", name, files[name], want)
				}
			}
			if updates.Load() != row.Observation.Updates {
				t.Fatalf("Source notifications %d want %d", updates.Load(), row.Observation.Updates)
			}
		})
	}
}
