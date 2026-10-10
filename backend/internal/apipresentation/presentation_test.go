package apipresentation

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

func originalDomainEnv() map[string]string {
	return map[string]string{"ANTHROPIC_BASE_URL": "https://127.0.0.1:1", "ANTHROPIC_API_KEY": "original-test-key", "CLAUDE_CODE_DISABLE_FAST_MODE": "1"}
}

func TestActualSourceTerminalContracts(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Env      map[string]string `json:"env"`
		Cases    []originalCase    `json:"cases"`
		Expected []fixtureRow      `json:"expected"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil || len(contract.Cases) == 0 || len(contract.Cases) != len(contract.Expected) {
		t.Fatal("invalid actual Source contract", err)
	}
	for index, item := range contract.Cases {
		t.Run(item.Name, func(t *testing.T) {
			// Only original constructor/env/options are inputs. Source expected
			// text, envelope and allocation observations never enter production.
			actual, err := runOriginal(contract.Env, item)
			if err != nil {
				t.Fatal(err)
			}
			if item.Supported != nil && !*item.Supported {
				if !actual.Capability || actual.Result != "" || len(actual.Allocations) != 0 || actual.UUIDCalls != item.PriorUUIDs {
					t.Fatal("unsupported presentation allocated or fabricated an envelope", actual)
				}
				return
			}
			if !reflect.DeepEqual(actual, contract.Expected[index]) {
				t.Fatalf("strict Source terminal bytes/allocation mismatch\nexpected %+v\nactual %+v", contract.Expected[index], actual)
			}
		})
	}
}

func TestOriginalDomainRejectsUnportedCredentialAndTLSBranches(t *testing.T) {
	for _, change := range []map[string]string{{"CLAUDE_CODE_USE_BEDROCK": "1"}, {"ANTHROPIC_API_KEY": ""}, {"ANTHROPIC_BASE_URL": ""}, {"CLAUDE_CODE_DISABLE_FAST_MODE": "0"}, {"NODE_OPTIONS": "--use-system-ca"}} {
		env := originalDomainEnv()
		for key, value := range change {
			env[key] = value
		}
		_, err := DomainFromOriginalEnv(env)
		var capability *CapabilityError
		if !errors.As(err, &capability) {
			t.Fatal("unported original configuration did not fail closed", change, err)
		}
		if !strings.HasPrefix(err.Error(), "API presentation capability: ") || !strings.Contains(err.Error(), capability.Boundary) || capability.Boundary == "" {
			t.Fatal("unported configuration lost its public diagnostic", err)
		}
	}
}

type nonComparableCause []string

func (nonComparableCause) Error() string { return "original noncomparable host error" }

type nativeCodeCause struct{}

func (nativeCodeCause) Error() string { return "original native cause" }
func (nativeCodeCause) Code() string  { return "FailedToOpenSocket" }

func TestInvalidAndUnsupportedPresentationDoesNotAdvanceSharedFactory(t *testing.T) {
	domain, err := DomainFromOriginalEnv(originalDomainEnv())
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	factory, _ := messages.NewFactory(func() string { calls++; return "shared" }, func() time.Time { calls++; return time.UnixMilli(1700000000000) })
	var absentSDK *provider.SDKStreamError
	status := 400
	for _, fault := range []error{nil, absentSDK, errors.New("consumer failure"), &retrypolicy.CannotRetryError{}, &provider.SDKStreamError{Class: "APIConnectionError", Status: &status}, &provider.SDKStreamError{Class: "APIConnectionError", Payload: json.RawMessage(`null`)}, &provider.SDKStreamError{Class: "APIConnectionError", MessageJSON: json.RawMessage(`4`)}, &provider.SDKStreamError{Class: "APIConnectionError", Message: "Connection error.", Cause: nonComparableCause{"original"}}} {
		if value, err := CreationFailure(fault, domain, factory); value != nil || err == nil {
			t.Fatal("unported original failure accepted", fault, value, err)
		}
	}
	for _, fixture := range []struct {
		domain  Domain
		factory *messages.Factory
	}{{Domain{}, factory}, {domain, nil}} {
		if value, err := CreationFailure(&provider.SDKStreamError{Class: "APIConnectionTimeoutError"}, fixture.domain, fixture.factory); value != nil || err == nil {
			t.Fatal(value, err)
		}
	}
	if calls != 0 {
		t.Fatal("unsupported or invalid domain advanced the session allocator", calls)
	}
	// The actual provider's native code interface takes the same original
	// cause path as the neutral raw-UTF16 constructor contract above.
	value, err := CreationFailure(&provider.SDKStreamError{Class: "APIConnectionError", Message: "Connection error.", Cause: nativeCodeCause{}}, domain, factory)
	if err != nil || value == nil || calls != 3 {
		t.Fatal(value, err, calls)
	}
}
