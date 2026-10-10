package messages

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func pairingProjection(history []storage.Object, paired PairingResult, err error) map[string]any {
	value := map[string]any{"historyAfter": history}
	if err != nil {
		var strict *PairingError
		if !errors.As(err, &strict) {
			panic(err)
		}
		value["error"] = strict.MessageJSON
		return value
	}
	value["messages"] = paired.Messages
	if paired.Repair != nil {
		value["repairEvent"] = map[string]any{"messageCount": paired.Repair.MessageCount, "repairedMessageCount": paired.Repair.RepairedMessageCount, "messageTypes": paired.Repair.MessageTypes}
		value["diagnostic"] = paired.Repair.Diagnostic
	}
	return value
}

func TestPairingRepairAndStrictRejectionMatchActualTypeScript(t *testing.T) {
	raw, err := os.ReadFile("testdata/pairing_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		SourceCommit string `json:"sourceCommit"`
		Cases        []struct {
			Name  string `json:"name"`
			Steps []struct {
				History   []storage.Object `json:"history"`
				Strict    bool             `json:"strict"`
				Universal bool             `json:"universal"`
			} `json:"steps"`
			Expected  []json.RawMessage `json:"expected"`
			UUIDCalls int               `json:"uuidCalls"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if contract.SourceCommit != "6b9d2aec1d9321a4dfff90d16b51f10c1ab984c8" || len(contract.Cases) < 15 {
		t.Fatal("missing actual TS pairing contracts")
	}
	for _, row := range contract.Cases {
		t.Run(row.Name, func(t *testing.T) {
			factory, calls := fixtureFactory(t)
			for index, step := range row.Steps {
				paired, err := EnsureToolResultPairing(step.History, factory, PairingOptions{Strict: step.Strict, UniversalSmoosh: step.Universal})
				if err != nil {
					var strict *PairingError
					if !errors.As(err, &strict) {
						t.Fatal(err)
					}
					// The terminal replacement representation must never be used
					// instead of MessageJSON in a model-visible error envelope.
					if strict.Error() == "" {
						t.Fatal("empty strict error")
					}
				}
				actual, failure := json.Marshal(pairingProjection(step.History, paired, err))
				if failure != nil {
					t.Fatal(failure)
				}
				actual, failure = protocol.ParseAndStringifyJavaScriptJSON(actual)
				if failure != nil {
					t.Fatal(failure)
				}
				sameJSON(t, row.Expected[index], actual, "$")
			}
			if *calls != row.UUIDCalls {
				t.Fatal("pairing changed session identity consumption", *calls, row.UUIDCalls)
			}
		})
	}
}

func TestPairingRequiresFactoryOnlyWhenNewEnvelopeIsNecessary(t *testing.T) {
	history := []storage.Object{options(`{"type":"assistant","message":{"id":"api","content":[{"type":"tool_use","id":"one","name":"fixture","input":{}}]}}`)}
	before, _ := json.Marshal(history)
	if _, err := EnsureToolResultPairing(history, nil, PairingOptions{}); err == nil {
		t.Fatal("missing session factory accepted for repair")
	}
	withOrphan := append(append([]storage.Object{}, history...), options(`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"orphan","content":"old"}]}}`))
	withOrphan[0] = options(`{"type":"assistant","message":{"id":"api","content":[{"type":"text","text":"hello"}]}}`)
	if _, err := EnsureToolResultPairing(withOrphan, nil, PairingOptions{}); err == nil {
		t.Fatal("missing session factory accepted for role-alternation repair")
	}
	after, _ := json.Marshal(history)
	if !bytes.Equal(before, after) {
		t.Fatal("failed pairing mutated original history")
	}
	valid := []storage.Object{options(`{"type":"user","message":{"content":"start"}}`)}
	if result, err := EnsureToolResultPairing(valid, nil, PairingOptions{}); err != nil || len(result.Messages) != 1 || result.Repair != nil {
		t.Fatal(result, err)
	}
}

func TestPairingRejectsMalformedInternalEnvelopesWithoutPartialHistory(t *testing.T) {
	factory, _ := fixtureFactory(t)
	for _, raw := range []string{`{"type":"user"}`, `{"type":"user","message":{"content":42}}`, `{"type":"assistant"}`, `{"type":"assistant","message":{"content":42}}`} {
		result, err := EnsureToolResultPairing([]storage.Object{options(raw)}, factory, PairingOptions{})
		if err == nil || result.Messages != nil {
			t.Fatal("malformed envelope accepted", raw, err)
		}
	}
	for _, raw := range []string{`{"type":"user"}`, `{"type":"user","message":{"content":42}}`} {
		history := []storage.Object{options(`{"type":"assistant","message":{"content":[{"type":"text","text":"hello"}]}}`), options(raw)}
		if _, err := EnsureToolResultPairing(history, factory, PairingOptions{}); err == nil {
			t.Fatal("malformed next envelope accepted", raw)
		}
	}
}
