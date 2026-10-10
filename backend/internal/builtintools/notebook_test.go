package builtintools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/queryloop"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

type notebookCorpus struct {
	SchemaSource             string
	Enabled, ConcurrencySafe bool
	Observations             []struct {
		Step struct {
			Name, Operation, InputSource, File, ContentBase64 string
			ReadBefore, Stale, SkipValidation                 bool
			Random                                            float64
		}
		Observation struct {
			Success                                                                       bool
			DataSource, IssuesSource, ValidationSource, ResultSource, MappedSource, Error string
			CacheSource                                                                   *string
			BeforeBase64, AfterBase64                                                     string
			RandomCalls                                                                   int
		}
	}
	SourceOnly struct {
		Consumer     string
		Capabilities struct{ Blocked []string }
	}
}

func sourceNotebookCorpus(t *testing.T) notebookCorpus {
	t.Helper()
	data, err := os.ReadFile("testdata/native_notebook_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus notebookCorpus
	if err := json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Observations) != 33 || !corpus.Enabled || corpus.ConcurrencySafe || len(corpus.SourceOnly.Capabilities.Blocked) > 0 || !strings.Contains(corpus.SourceOnly.Consumer, "actual NotebookEditTool") {
		t.Fatal("missing actual Source Notebook capture")
	}
	return corpus
}
func TestActualNotebookSchemaPromptAndInputMatchSource(t *testing.T) {
	corpus := sourceNotebookCorpus(t)
	d, err := toolschema.NativeBuiltinDefinition("NotebookEdit")
	if err != nil {
		t.Fatal(err)
	}
	schemas, err := toolschema.NewSession().RenderBatch([]toolschema.Definition{d}, toolschema.Options{Host: toolschema.Host{BaseURL: "http://127.0.0.1:1"}, Model: "deepseek-flash"})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemas) != 1 || !jsonSame(raw(schemas[0]), []byte(corpus.SchemaSource)) {
		t.Fatalf("actual schema/prompt %s != Source %s", raw(schemas[0]), corpus.SchemaSource)
	}
	for _, row := range corpus.Observations {
		if row.Step.Operation == "read" {
			continue
		}
		t.Run(row.Step.Name, func(t *testing.T) {
			parsed, err := toolschema.ParseInputJSON(d, []byte(row.Step.InputSource))
			if row.Observation.Success {
				if err != nil || !jsonSame(raw(parsed), []byte(row.Observation.DataSource)) {
					t.Fatalf("parsed %s %v != %s", raw(parsed), err, row.Observation.DataSource)
				}
			} else {
				var validation *toolschema.ValidationError
				if !errors.As(err, &validation) || !jsonSame(validation.Issues, []byte(row.Observation.IssuesSource)) {
					t.Fatalf("parser error %v != %s", err, row.Observation.IssuesSource)
				}
			}
		})
	}
}

func TestActualNotebookCallsFilesCacheAndResultsMatchSource(t *testing.T) {
	corpus := sourceNotebookCorpus(t)
	for _, row := range corpus.Observations {
		if row.Step.Operation == "parse" {
			continue
		}
		t.Run(row.Step.Name, func(t *testing.T) {
			s, owner, home, _ := fixture(t)
			s.options.NonInteractive = true
			randomCalls := 0
			s.options.Random = func() float64 { randomCalls++; return row.Step.Random }
			path := filepath.Join(home, row.Step.File)
			before, err := base64.StdEncoding.DecodeString(row.Step.ContentBase64)
			if err != nil {
				t.Fatal(err)
			}
			if err = os.WriteFile(path, before, 0640); err != nil {
				t.Fatal(err)
			}
			stamp, err := mtime(path)
			if err != nil {
				t.Fatal(err)
			}
			if row.Step.Stale {
				future := time.Now().Add(2 * time.Second)
				if err = os.Chtimes(path, future, future); err != nil {
					t.Fatal(err)
				}
			}
			name := "NotebookEdit"
			if row.Step.Operation == "read" {
				name = "Read"
			}
			def, err := toolschema.NativeBuiltinDefinition(name)
			if err != nil {
				t.Fatal(err)
			}
			parsed, err := toolschema.ParseInputJSON(def, []byte(row.Step.InputSource))
			if err != nil {
				t.Fatal(err)
			}
			var result json.RawMessage
			var validation json.RawMessage
			var cache json.RawMessage
			err = owner.Run(context.Background(), func(scope *cooperative.Scope) error {
				if row.Step.ReadBefore {
					offset := 1.0
					if err := s.options.Cache.Set(scope, path, builtinwrite.FileState{Content: quote("original read"), Timestamp: stamp, Offset: &offset}); err != nil {
						return err
					}
				}
				if name == "NotebookEdit" && !row.Step.SkipValidation {
					v := s.Validate(context.Background(), name, parsed, scope)
					if v == nil {
						validation = object(field("result", true))
					} else {
						var typed *builtinwrite.ValidationError
						if !errors.As(v, &typed) {
							return v
						}
						validation = object(field("result", false), field("message", typed.Message), field("errorCode", typed.ErrorCode))
					}
					if row.Step.Operation == "validate" || string(input(string(validation)).Get("result")) == "false" {
						return nil
					}
				}
				_, tools, err := s.Catalog(scope)
				if err != nil {
					return err
				}
				var tool *queryloop.RegisteredTool
				for i := range tools {
					if tools[i].Name == name {
						tool = &tools[i]
					}
				}
				if tool == nil || tool.IsConcurrencySafe(parsed) != (name == "Read") {
					return fmt.Errorf("actual notebook tool catalog/concurrency changed")
				}
				called, err := tool.CallJSON(context.Background(), parsed, queryloop.JSONToolCallContext{Opaque: &builtinwrite.CallContext{Scope: scope}, ToolUseID: quote("actual-notebook-tool-use-id")})
				if err != nil {
					return err
				}
				result = called.Data
				state, ok, err := s.options.Cache.Get(scope, path)
				if err != nil {
					return err
				}
				if ok {
					projected := protocol.NewJSONObject(protocol.JSONField{Name: "content", Value: state.Content})
					if state.Offset != nil {
						projected.Set("offset", raw(*state.Offset))
					}
					if state.Limit != nil {
						projected.Set("limit", raw(*state.Limit))
					}
					if state.PartialView {
						projected.Set("partialView", raw(true))
					}
					current, err := mtime(path)
					if err != nil {
						return err
					}
					projected.Set("timestampMatchesFile", raw(current == state.Timestamp))
					cache = raw(projected)
				}
				return nil
			})
			if row.Observation.Error != "" {
				if err == nil || err.Error() != row.Observation.Error {
					t.Fatalf("actual error %v != Source %s", err, row.Observation.Error)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if row.Observation.ValidationSource != "" && !jsonSame(validation, []byte(row.Observation.ValidationSource)) {
				t.Fatalf("validation %s != Source %s", validation, row.Observation.ValidationSource)
			}
			if row.Observation.ResultSource != "" {
				want := []byte(row.Observation.ResultSource)
				if name == "NotebookEdit" {
					original := input(row.Observation.ResultSource).String("notebook_path")
					want = []byte(strings.ReplaceAll(string(want), filepath.Dir(original), home))
				}
				if !jsonSame(result, want) {
					t.Fatalf("result %s != Source %s", result, want)
				}
				mapped, err := MapResult(name, result, quote("actual-notebook-tool-use-id"))
				if err != nil {
					t.Fatal(err)
				}
				if !jsonSame(raw(mapped), []byte(row.Observation.MappedSource)) {
					t.Fatalf("mapped %s != Source %s", raw(mapped), row.Observation.MappedSource)
				}
				if row.Observation.CacheSource != nil && !jsonSame(cache, []byte(*row.Observation.CacheSource)) {
					t.Fatalf("cache %s != Source %s", cache, *row.Observation.CacheSource)
				}
			}
			after, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if actual := base64.StdEncoding.EncodeToString(after); actual != row.Observation.AfterBase64 {
				t.Fatalf("actual disk bytes differ: %s != Source %s", actual, row.Observation.AfterBase64)
			}
			if row.Observation.BeforeBase64 != base64.StdEncoding.EncodeToString(before) {
				t.Fatal("before file differs from Source capture")
			}
			if randomCalls != row.Observation.RandomCalls {
				t.Fatalf("actual Math.random calls %d != %d", randomCalls, row.Observation.RandomCalls)
			}
			stat, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if stat.Mode().Perm() != 0640 {
				t.Fatal("Notebook write lost existing file mode")
			}
		})
	}
}
