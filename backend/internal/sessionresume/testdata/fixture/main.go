package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionresume"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"
)

func fail() error {
	return errors.New("Session resume fixture requires disposable HOME/config and ordinary private files")
}
func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func assertHome(home string) error {
	if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) || home != os.Getenv("HOME") || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return fail()
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(root) {
		return fail()
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || canonical == canonicalRoot || !inside(canonicalRoot, canonical) {
		return fail()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail()
	}
	canonicalCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil || canonicalCwd != canonical {
		return fail()
	}
	config := filepath.Join(home, ".claude")
	if info, err := os.Lstat(config); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonicalConfig, err := filepath.EvalSymlinks(config)
	if err != nil || !inside(canonical, canonicalConfig) {
		return fail()
	}
	return nil
}
func guard(home, path string, ordinary bool) error {
	if !inside(home, path) {
		return fail()
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	for probe := path; ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		if errors.Is(err, os.ErrNotExist) {
			if probe == filepath.Dir(probe) {
				return fail()
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || probe == path && ordinary && !info.Mode().IsRegular() {
			return fail()
		}
		canonical, err := filepath.EvalSymlinks(probe)
		if err != nil || !inside(canonicalHome, canonical) {
			return fail()
		}
		return nil
	}
}

type privateFS struct{ home string }

func (files privateFS) Open(path string) (sessionstore.File, error) {
	if err := guard(files.home, path, true); err != nil {
		panic(err)
	}
	return (sessionstore.OSFileSystem{}).Open(path)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}

type input struct {
	Home       string `json:"home"`
	Classifier bool   `json:"classifier"`
	Steps      []struct {
		Operation string   `json:"operation"`
		Sources   []string `json:"sources"`
		Bytes     string   `json:"bytes"`
	} `json:"steps"`
}

func source(value any) string {
	raw, err := json.Marshal(value)
	must(err)
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	must(err)
	return string(raw)
}
func failure(err error) map[string]any {
	var typed *sessionresume.Failure
	if errors.As(err, &typed) {
		return map[string]any{"name": typed.Name, "message": typed.MessageValue()}
	}
	return map[string]any{"name": "Error", "message": err.Error()}
}
func main() {
	var input input
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	must(decoder.Decode(&input))
	if decoder.Decode(new(any)) != io.EOF {
		panic("single input required")
	}
	must(assertHome(input.Home))
	if os.Getenv("CC_HAHA_SESSION_RESUME_FIXTURE") != "1" || os.Getenv("NODE_ENV") != "production" || input.Steps == nil {
		panic("Session resume requires original explicit offline inputs")
	}
	processCwd, err := os.Getwd()
	must(err)
	path, output := filepath.Join(input.Home, "session.jsonl"), filepath.Join(input.Home, "resume-result.json")
	must(guard(input.Home, path, true))
	must(guard(input.Home, output, true))
	observations := []map[string]any{}
	rawErrors := []map[string]any{}
	for _, step := range input.Steps {
		allocations, diagnostics, errorRows := []map[string]any{}, []map[string]any{}, []map[string]any{}
		calls := 0
		now := func() time.Time {
			allocations = append(allocations, map[string]any{"kind": "time", "value": "2023-11-14T22:13:20.000Z"})
			return time.UnixMilli(1700000000000)
		}
		factory, err := messages.NewFactory(func() string {
			calls++
			value := fmt.Sprintf("00000000-0000-4000-8000-%012d", calls)
			allocations = append(allocations, map[string]any{"kind": "uuid", "value": value})
			return value
		}, now)
		must(err)
		capture := func(err error) {
			row := failure(err)
			row["stack"] = string(debug.Stack())
			rawErrors = append(rawErrors, row)
			errorRows = append(errorRows, failure(err))
		}
		reducer, err := sessionresume.New(sessionresume.Options{Factory: factory, TranscriptClassifier: input.Classifier, RelativePath: func(path json.RawMessage) (json.RawMessage, error) {
			return sessionresume.RelativePath(input.Home, processCwd, path)
		}, OnDiagnostic: func(event sessionresume.Diagnostic) {
			diagnostics = append(diagnostics, map[string]any{"kind": event.Kind, "name": event.Name, "fields": event.Fields})
		}, OnError: func(err error) { now(); capture(err) }})
		must(err)
		loader, err := sessionhistory.New(sessionhistory.Options{Files: privateFS{input.Home}, Stat: func(path string) (int64, error) {
			must(guard(input.Home, path, false))
			info, err := os.Stat(path)
			if err != nil {
				return 0, err
			}
			return info.Size(), nil
		}, ReadFile: func(path string) ([]byte, error) { must(guard(input.Home, path, false)); return os.ReadFile(path) }, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) {
			if event.Message != "" {
				now()
				capture(errors.New(event.Message))
			}
			if event.Code != "" {
				diagnostics = append(diagnostics, map[string]any{"kind": "event", "name": event.Code, "fields": event.Fields})
			}
		}})
		must(err)
		history := []*sessionresume.Message{}
		var result any
		var reduced sessionresume.Result
		var operationErr error
		switch step.Operation {
		case "rewrite", "append":
			must(guard(input.Home, path, true))
			bytes, err := hex.DecodeString(step.Bytes)
			must(err)
			if step.Operation == "rewrite" {
				operationErr = os.WriteFile(path, bytes, 0600)
			} else {
				file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
				operationErr = err
				if err == nil {
					_, operationErr = file.Write(bytes)
					must(file.Close())
				}
			}
			result = map[string]any{"ok": true}
		case "reduce":
			raws := []json.RawMessage{}
			for _, raw := range step.Sources {
				raws = append(raws, json.RawMessage(raw))
			}
			history, operationErr = sessionresume.ParseMessages(raws)
			if operationErr == nil {
				reduced, operationErr = reducer.Deserialize(history)
				result = reduced
			}
		case "path":
			must(guard(input.Home, path, true))
			loaded, err := reducer.LoadPath(loader, path)
			operationErr = err
			history = loaded.Input
			if history == nil {
				history = []*sessionresume.Message{}
			}
			reduced = loaded.Result
			result = loaded
		default:
			operationErr = errors.New("Unknown session resume operation")
		}
		var errorValue any
		var resultValue any
		if operationErr != nil {
			capture(operationErr)
			errorValue = failure(operationErr)
		} else {
			resultValue = source(result)
		}
		references := []int{}
		var interruptionIndex any
		for index, message := range reduced.Messages {
			reference := -1
			for inputIndex, original := range history {
				if message == original {
					reference = inputIndex
					break
				}
			}
			references = append(references, reference)
			if reduced.TurnInterruptionState.Message == message {
				interruptionIndex = index
			}
		}
		permissions := []map[string]any{}
		for _, message := range history {
			if string(message.Field("type")) == `"user"` {
				var id, value any
				if raw := message.UUID(); raw != nil {
					must(json.Unmarshal(raw, &id))
				}
				if raw := message.Field("permissionMode"); raw != nil {
					must(json.Unmarshal(raw, &value))
				}
				permissions = append(permissions, map[string]any{"uuid": id, "own": message.HasOwnField("permissionMode"), "value": value})
			}
		}
		observations = append(observations, map[string]any{"resultSource": resultValue, "historyAfterSource": source(history), "references": references, "interruptionIndex": interruptionIndex, "permissionOwn": permissions, "allocations": allocations, "diagnostics": diagnostics, "errors": errorRows, "failure": errorValue})
	}
	must(guard(input.Home, output, true))
	must(os.WriteFile(output, []byte(source(map[string]any{"observations": observations, "sourceOnly": map[string]any{"rawErrors": rawErrors}})), 0600))
	fmt.Print(`{"result":"resume-result.json"}`)
}
