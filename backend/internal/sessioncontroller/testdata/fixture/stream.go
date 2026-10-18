package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkhost"
	"github.com/NanmiCoder/cc-haha/backend/internal/securestorage"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessioncontroller"
	"github.com/NanmiCoder/cc-haha/backend/internal/tracecapture"
)

type structuredObservation struct {
	Requests         []string                   `json:"requests"`
	PreparedRequests []string                   `json:"preparedRequests"`
	Output           []string                   `json:"output"`
	NativeFetch      []string                   `json:"nativeFetch"`
	Diagnostics      string                     `json:"diagnostics"`
	NativeFiles      []clibootstrap.NativeEvent `json:"nativeFiles"`
	ResumePicker     []protocol.JSONObject      `json:"resumePicker"`
}

func dynamicSDK(input input) bool {
	if input.NativeMain {
		return true
	}
	if len(input.SDK) > 1 {
		for _, sdk := range input.SDK {
			if sdk.String("priority") == "now" {
				return true
			}
		}
	}
	return false
}

// Dynamic SDK inputs enter the ordinary native stdin producer and the actual
// NativeMainPrint-owned Controller/Harness. No prepared Source state, ordered
// writer shim, or alternate bare-SDK input processor participates in this path.
func runNativeStructuredController(input input, uuid func() string, now func() time.Time, phase func(string)) (map[string]any, structuredObservation) {
	env := map[string]string{}
	for _, pair := range os.Environ() {
		key, value, ok := strings.Cut(pair, "=")
		if ok {
			env[key] = value
		}
	}
	env["HOME"], env["CLAUDE_CONFIG_DIR"] = input.Home, filepath.Join(input.Home, ".claude")
	env["USER_TYPE"], env["CLAUDE_CODE_SIMPLE"] = "external", "1"
	env["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"] = source(input.Checkpointing)
	env["CLAUDE_CODE_RESUME_INTERRUPTED_TURN"] = input.ResumeInterrupted
	native, err := clibootstrap.NewProcessNativeBoundary(clibootstrap.NativeProcessOptions{Environment: env, CWD: input.Home, Platform: input.Runtime.Platform, ManagedRoot: filepath.Join(input.Home, "policy"), ReadMDM: func() (*protocol.JSONObject, *protocol.JSONObject, error) { return nil, nil, nil }})
	must(err)
	native.Now, native.NowMillis = now, func() float64 { return float64(now().UnixMilli()) }
	native.RunSecurity = func(context.Context, securestorage.Command) ([]byte, error) { return nil, os.ErrNotExist }
	observed := structuredObservation{Requests: []string{}, PreparedRequests: []string{}, Output: []string{}, NativeFetch: []string{}}
	if input.NativeMain {
		picker, err := clibootstrap.NewCLIResumePicker(context.Background(), native, input.Runtime.Platform, nil)
		must(err)
		all := false
		observed.ResumePicker, _, err = picker.ReadResumePicker(context.Background(), &all, 10)
		must(err)
	}
	var fetchBody []byte
	var diagnostics bytes.Buffer
	var print *clibootstrap.MainPrint
	activationHistory := []protocol.JSONObject{}
	cache := []protocol.JSONObject{}
	var appState sessioncontroller.AppState
	uuidChecks := []map[string]any{}
	var stdin bytes.Buffer
	for _, message := range input.SDK {
		stdin.WriteString(source(message) + "\n")
	}
	disabled := false
	arguments := clibootstrap.PrintArguments{ComputerUse: &disabled, SessionID: input.Temporary, Resume: input.SID, Continue: input.Continue, ForkSession: input.ForkSession, ResumeAt: input.ResumeAt, SessionPersistenceDisabled: input.Disabled, Model: input.Model, SystemPrompt: "original custom system", HasCustomSystemPrompt: true, Tools: []string{""}, AllowedTools: []string{}, MaxTurns: 1, Replay: true, Sources: clibootstrap.DefaultState(), PermissionMode: "default", Thinking: requestcontext.Thinking{Type: "disabled"}}
	if input.PickerSelection {
		found := false
		for _, row := range observed.ResumePicker {
			if row.String("sessionId") == input.SID {
				// The actual CLI picker passes this row's fullPath to activation.
				arguments.Resume = row.String("fullPath")
				found = arguments.Resume != ""
				break
			}
		}
		if !found {
			panic("actual native picker did not contain the owned session")
		}
	}
	print, err = clibootstrap.NewMainPrint(clibootstrap.MainPrintOptions{PublicPrintLoadErrors: true, Arguments: arguments, Native: native, PID: os.Getpid(), Version: "fixture-version", Platform: input.Runtime.Platform, Input: &stdin, Output: io.Discard, Error: &diagnostics, UUIDPrimitive: uuid, SDKRuntime: &input.Runtime, OnControllerPhase: func(value string) {
		phase(value)
		if value == "ask:start" {
			activationHistory = print.Controller.History()
			cache = print.Controller.ReadCache().Entries()
			appState = print.Controller.AppState()
		}
	}, OnSDKPhase: phase, OnSDKUUIDLookup: func(id json.RawMessage, historical bool) {
		uuidChecks = append(uuidChecks, map[string]any{"uuid": id, "exists": historical})
	}, OnSDKRecord: func() { phase("record") }, ObserveModelFailure: func(failure error) {
		observed.Diagnostics += fmt.Sprintf("%T: %v\n", failure, failure)
	}, ObserveSDKRequest: func(_ string, _ http.Header, body json.RawMessage) {
		observed.PreparedRequests = append(observed.PreparedRequests, string(body))
	}, ObserveSDKFetch: func(input tracecapture.FetchInput) {
		fetchBody = append([]byte(nil), input.PlainBody...)
	}, ObserveSDKNativeFetch: func(event provider.OwnerNativeEvent) {
		observed.NativeFetch = append(observed.NativeFetch, source(map[string]any{"kind": event.Kind, "status": event.Status}))
		if event.Kind == "fetch-fulfilled" {
			observed.Requests = append(observed.Requests, string(fetchBody))
			phase("HTTP")
		}
	}, OnOutput: func(value []byte) error { observed.Output = append(observed.Output, string(value)); return nil }, WrapSDKASK: func(inner *sdkhost.OwnerIterator[[]byte]) (*sdkhost.OwnerIterator[[]byte], error) {
		// This is the same real yield* wrapper used by the Source observer.
		return sdkhost.NewOwnerIterator(print.Owner, func(scope *cooperative.Scope, iterator *sdkhost.OwnerIterator[[]byte], request sdkhost.IteratorRequest[[]byte]) error {
			return iterator.Delegate(scope, inner, request, func(scope *cooperative.Scope, _ sdkhost.IteratorResult[[]byte], request sdkhost.IteratorRequest[[]byte]) error {
				if request.Returning {
					return iterator.CompleteReturn(scope)
				}
				return iterator.Complete(scope)
			}, func(scope *cooperative.Scope, err error) error { return iterator.Fail(scope, err) })
		})
	}})
	must(err)
	// The original Source fixture has async UUID/Record/Flush spies. Bind only
	// this observer layer before the real Run, retaining original values/errors.
	must(print.Owner.Run(context.Background(), print.Controller.BindSDKAsyncReturnObservers))
	code, err := print.Run(context.Background())
	if err != nil {
		must(os.WriteFile(filepath.Join(input.Home, "native-failed-controller.json"), []byte(source(map[string]any{"failure": err.Error(), "stream": observed})), 0600))
	}
	must(err)
	if code != 0 {
		panic("NativeMainPrint returned nonzero exit")
	}
	observed.Diagnostics += diagnostics.String()
	observed.NativeFiles = native.Trace
	history := []string{}
	for _, message := range print.Host.History() {
		// Source JSON.stringify observes the original live assistant envelope's
		// late message/content/usage references, as the same ASK and Writer do.
		// Controller.History intentionally exposes only its stored raw records.
		if message.Assistant != nil {
			data, err := message.Assistant.MarshalJSON()
			must(err)
			history = append(history, string(data))
		} else {
			history = append(history, source(message.Record))
		}
	}
	observation := map[string]any{"activationHistory": activationHistory, "snapshotRecords": print.Controller.SnapshotRecords(), "background": map[string]bool{}, "appState": appState, "cache": cache, "uuidChecks": uuidChecks, "enqueued": print.Controller.QueueEvents(), "replays": print.Controller.Replays(), "historySource": history, "requests": observed.Requests, "transcripts": nativeFixtureFiles(input.Home, "projects"), "errors": []string{}, "copies": nativeFixtureFiles(input.Home, "file-history")}
	return observation, observed
}

func nativeFixtureFiles(home, kind string) map[string]string {
	files := map[string]string{}
	must(filepath.WalkDir(filepath.Join(home, ".claude", kind), func(path string, entry fs.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		must(guard(home, path, false))
		if entry.Type().IsRegular() {
			data, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			name, err := filepath.Rel(home, path)
			if err != nil {
				return err
			}
			files[name] = string(data)
		}
		return nil
	}))
	return files
}
