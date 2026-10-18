package sdkhost

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
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type structuredReturnProfile struct {
	Name          string   `json:"name"`
	Chunks        []string `json:"chunks"`
	Consumer      string   `json:"consumer"`
	Prepend       bool     `json:"prepend"`
	ReturnDefined bool     `json:"returnDefined"`
	BodyError     bool     `json:"bodyError"`
	FinallyMode   string   `json:"finallyMode"`
}

func structuredReturnProfiles() []structuredReturnProfile {
	line := `{"type":"user","uuid":"actual-first","message":{"role":"user","content":"actual original bytes"}}`
	second := `{"type":"user","uuid":"actual-second","message":{"role":"user","content":"must remain unconsumed"}}`
	profiles := []structuredReturnProfile{}
	for _, defined := range []bool{false, true} {
		for _, stage := range []string{"before", "prepend", "block", "final", "queued"} {
			profile := structuredReturnProfile{Name: fmt.Sprintf("%s-defined-%t", stage, defined), Chunks: []string{line + "\n" + second + "\n"}, Consumer: "return", ReturnDefined: defined}
			switch stage {
			case "before":
				profile.Consumer = "before"
			case "prepend":
				profile.Prepend = true
			case "final":
				profile.Chunks = []string{line}
			case "queued":
				profile.Consumer = "queued"
			}
			profiles = append(profiles, profile)
		}
	}
	profiles = append(profiles,
		structuredReturnProfile{Name: "normal-eof", Chunks: []string{line + "\n"}, Consumer: "drain"},
		structuredReturnProfile{Name: "normal-final-eof", Chunks: []string{line}, Consumer: "drain"},
		structuredReturnProfile{Name: "body-error-identity", Chunks: []string{line + "\n"}, Consumer: "drain", BodyError: true},
		structuredReturnProfile{Name: "body-finally-error-identity", Chunks: []string{line + "\n"}, Consumer: "drain", BodyError: true, FinallyMode: "throw"},
		structuredReturnProfile{Name: "return-finally-error-identity", Chunks: []string{line + "\n"}, Consumer: "return", FinallyMode: "throw"},
		structuredReturnProfile{Name: "return-finally-yield-once", Chunks: []string{line + "\n"}, Consumer: "return", FinallyMode: "yield"},
	)
	return profiles
}

func captureStructuredReturnSource(t *testing.T, profiles []structuredReturnProfile) []map[string]any {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun is required; this regression must not silently skip")
	}
	root, err := filepath.Abs("../../..")
	if err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	temp := filepath.Join(home, "tmp")
	if err = os.Mkdir(temp, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := os.Getenv("CC_HAHA_STRUCTURED_RETURN_FIXTURE")
	if fixture == "" {
		fixture, err = filepath.Abs("testdata/structured-return.fixture.ts")
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + temp, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_STRUCTURED_RETURN_CONTRACT=1", "BUN_OPTIONS=--no-env-file", "NODE_ENV=production", "ANTHROPIC_API_KEY=offline-fixture-key", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin", "TZ=UTC"}
	body, err := json.Marshal(map[string]any{"home": home, "sourceRoot": root, "profiles": profiles, "headless": false})
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdin = bytes.NewReader(body)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	data, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source return fixture: %v %.6000s", err, stderr.String())
	}
	t.Logf("actual Source constructor return capture: %s", data)
	var result struct {
		Rows    []map[string]any `json:"rows"`
		Blocked []any            `json:"blocked"`
	}
	if err = json.Unmarshal(data, &result); err != nil || len(result.Rows) != len(profiles) || len(result.Blocked) != 0 {
		t.Fatalf("actual Source observation: %v %.6000s", err, data)
	}
	return result.Rows
}

func observeStructuredReturn(t *testing.T, profile structuredReturnProfile) map[string]any {
	t.Helper()
	owner := cooperative.New()
	defer owner.Close()
	counters := map[string]int{"entered": 0, "yielded": 0, "resumed": 0, "finallyEntered": 0, "finallyCompleted": 0}
	events := []string{}
	results := []map[string]any{}
	bodyError, closeError := errors.New("actual-raw-body-error"), errors.New("actual-raw-finally-error")
	index, uuids := 0, 0
	started, awaitingYield := false, false
	var step GeneratorContinuation[string]
	finish := func(scope *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string], failure error) error {
		counters["finallyEntered"]++
		events = append(events, "raw:finally")
		if profile.FinallyMode == "throw" {
			return closeError
		}
		if profile.FinallyMode == "yield" {
			events = append(events, "raw:finally-yield")
			return iterator.Yield(scope, "actual-finally-yield", func(next *cooperative.Scope, it *OwnerIterator[string], _ IteratorRequest[string]) error {
				counters["finallyCompleted"]++
				events = append(events, "raw:finally-complete")
				if failure != nil {
					return failure
				}
				if request.Returning {
					return it.CompleteDefined(next, request.Value, request.Defined)
				}
				return it.Complete(next)
			})
		}
		counters["finallyCompleted"]++
		events = append(events, "raw:finally-complete")
		if failure != nil {
			return failure
		}
		if request.Returning {
			return iterator.CompleteReturn(scope)
		}
		return iterator.Complete(scope)
	}
	step = func(scope *cooperative.Scope, iterator *OwnerIterator[string], request IteratorRequest[string]) error {
		if !started {
			started = true
			counters["entered"]++
			events = append(events, "raw:entry")
		}
		if request.Returning {
			return finish(scope, iterator, request, nil)
		}
		if awaitingYield {
			counters["resumed"]++
			events = append(events, "raw:resumed")
			awaitingYield = false
		}
		if index == len(profile.Chunks) {
			var failure error
			if profile.BodyError {
				failure = bodyError
			}
			return finish(scope, iterator, request, failure)
		}
		chunk := profile.Chunks[index]
		index++
		counters["yielded"]++
		events = append(events, "raw:yield")
		awaitingYield = true
		return iterator.Yield(scope, chunk, step)
	}
	rawIterator, err := NewOwnerIterator(owner, step)
	if err != nil {
		t.Fatal(err)
	}
	input, err := NewPlainStructuredInput(owner, rawIterator)
	if err != nil {
		t.Fatal(err)
	}
	host := &Host{options: Options{Owner: owner, UUID: func() string { uuids++; return fmt.Sprint(uuids) }, OnPhase: func(string) {}, Output: Output{Enqueue: func([]byte) error { return nil }}}, sandboxInputOpen: true}
	closes := 0
	input.sandboxClose = func(scope *cooperative.Scope) error { closes++; return host.closeNativeSandboxInput(scope) }
	observation := map[string]any{}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		if profile.Prepend {
			if err := input.PrependUserMessage(scope, json.RawMessage(`"actual-prepended-prompt"`)); err != nil {
				return err
			}
		}
		sandbox, err := host.startSDKNativeSandboxPermission(context.Background(), scope, "pending.return.test")
		if err != nil {
			return err
		}
		consume := func(pending *OwnerFuture[IteratorResult[protocol.JSONObject]]) (map[string]any, error) {
			var outcome map[string]any
			// This dependent completion is the Source async consume helper's
			// plain return; its caller awaits that actual second boundary.
			completion, err := pending.Then(scope, func(_ *cooperative.Scope, result IteratorResult[protocol.JSONObject], failure error) error {
				if failure != nil {
					kind := "other"
					if failure == bodyError {
						kind = "body-identity"
					}
					if failure == closeError {
						kind = "close-identity"
					}
					outcome = map[string]any{"error": kind, "message": failure.Error()}
				} else {
					outcome = map[string]any{"done": result.Done, "defined": result.Defined}
					if result.Defined {
						outcome["value"] = result.Value
					}
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			if _, err = completion.Await(scope); err != nil {
				return nil, err
			}
			results = append(results, outcome)
			return outcome, nil
		}
		next := func() (*OwnerFuture[IteratorResult[protocol.JSONObject]], error) { return input.Next(scope) }
		returning := func() (*OwnerFuture[IteratorResult[protocol.JSONObject]], error) {
			if profile.ReturnDefined {
				return input.Iterator().Return(scope, protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: json.RawMessage(`"actual-return-value"`)}))
			}
			return input.Iterator().ReturnUndefined(scope)
		}
		capture := func(pending *OwnerFuture[IteratorResult[protocol.JSONObject]], failure error) (map[string]any, error) {
			if failure != nil {
				return nil, failure
			}
			return consume(pending)
		}
		switch profile.Consumer {
		case "before":
			if _, err = capture(returning()); err != nil {
				return err
			}
			if _, err = capture(next()); err != nil {
				return err
			}
		case "queued":
			first, err := next()
			if err != nil {
				return err
			}
			returned, err := returning()
			if err != nil {
				return err
			}
			last, err := next()
			if err != nil {
				return err
			}
			for _, pending := range []*OwnerFuture[IteratorResult[protocol.JSONObject]]{first, returned, last} {
				if _, err = consume(pending); err != nil {
					return err
				}
			}
		case "drain":
			for index := 0; index < 8; index++ {
				result, err := capture(next())
				if err != nil {
					return err
				}
				if result["error"] != nil || result["done"] == true {
					break
				}
				if index == 7 {
					return errors.New("unexpected nonterminal Go raw")
				}
			}
			if _, err = capture(next()); err != nil {
				return err
			}
		default:
			if _, err = capture(next()); err != nil {
				return err
			}
			if _, err = capture(returning()); err != nil {
				return err
			}
			if _, err = capture(next()); err != nil {
				return err
			}
		}
		var sandboxResult, newResult any
		normalEOF := profile.Consumer == "drain" && !profile.BodyError && profile.FinallyMode != "throw"
		if normalEOF {
			value, err := sandbox.Await(scope)
			if err != nil {
				return err
			}
			sandboxResult = value
		}
		pendingBeforeNew := len(host.sandboxPending)
		post, err := host.startSDKNativeSandboxPermission(context.Background(), scope, "new.return.test")
		if err != nil {
			return err
		}
		if normalEOF {
			value, err := post.Await(scope)
			if err != nil {
				return err
			}
			newResult = value
		}
		if closes != map[bool]int{true: 1, false: 0}[normalEOF] {
			return fmt.Errorf("normal EOF callback on wrong completion: %d/%t", closes, normalEOF)
		}
		observation = map[string]any{"name": profile.Name, "results": results, "events": events, "counters": counters, "pendingBeforeNew": pendingBeforeNew, "pendingAfterNew": len(host.sandboxPending), "sandboxResult": sandboxResult, "newResult": newResult}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	// JSON comparison changes object key order only and preserves event order,
	// full values, undefined descriptors, errors and every counter.
	encoded, err := json.Marshal(observation)
	if err != nil {
		t.Fatal(err)
	}
	var normalized map[string]any
	if err = json.Unmarshal(encoded, &normalized); err != nil {
		t.Fatal(err)
	}
	return normalized
}

func TestPlainStructuredInputActualSourceReturnContract(t *testing.T) {
	profiles := structuredReturnProfiles()
	first := captureStructuredReturnSource(t, profiles)
	second := captureStructuredReturnSource(t, profiles)
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("actual Source replay changed: first%v second%v", first, second)
	}
	for index, profile := range profiles {
		t.Run(profile.Name, func(t *testing.T) {
			actual := observeStructuredReturn(t, profile)
			if !reflect.DeepEqual(actual, first[index]) {
				want, _ := json.Marshal(first[index])
				got, _ := json.Marshal(actual)
				t.Fatalf("actual Source/Go Return mismatch\nSource%s\nGo%s", want, got)
			}
		})
	}
}
