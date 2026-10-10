package clisettings

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"sync"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestActualSourceSettingsStateContract(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "state_contract.json"))
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Input  stateInput `json:"input"`
		Result struct {
			Observations []json.RawMessage `json:"observations"`
		} `json:"result"`
	}
	if err := json.Unmarshal(raw, &contract); err != nil || len(contract.Input.Cases) != 5 || len(contract.Result.Observations) != 5 {
		t.Fatalf("missing actual Source state contract: %v", err)
	}
	for index, scenario := range contract.Input.Cases {
		t.Run(scenario.Name, func(t *testing.T) {
			observed, err := observeState(contract.Input, scenario)
			if err != nil {
				t.Fatal(err)
			}
			encoded, err := json.Marshal(observed)
			if err != nil {
				t.Fatal(err)
			}
			var actual, expected any
			_ = json.Unmarshal(encoded, &actual)
			_ = json.Unmarshal(contract.Result.Observations[index], &expected)
			if !reflect.DeepEqual(expected, actual) {
				t.Fatalf("actual Source state differs\nwant %s\ngot %s", contract.Result.Observations[index], encoded)
			}
		})
	}
}

func TestRuntimeStateConcurrentIndependentWriteMarksAndCaches(t *testing.T) {
	state, err := NewState(func() float64 { return 1000 })
	if err != nil {
		t.Fatal(err)
	}
	var pending sync.WaitGroup
	for index := 0; index < 32; index++ {
		pending.Add(1)
		go func(index int) {
			defer pending.Done()
			key := strconv.Itoa(index)
			object := protocol.NewJSONObject()
			state.SetCachedSettingsForSource(key, &object)
			state.MarkInternalWrite(key)
			if value, exists := state.GetCachedSettingsForSource(key); !exists || value != &object {
				t.Error("runtime lost cache reference", index)
			}
			if !state.ConsumeInternalWrite(key, 1) || state.ConsumeInternalWrite(key, 1) {
				t.Error("runtime write mark consumed incorrectly", index)
			}
		}(index)
	}
	pending.Wait()
}

func TestSettingsStateRequiresExplicitClock(t *testing.T) {
	if state, err := NewState(nil); state != nil || err == nil {
		t.Fatal("ambient clock was silently used")
	}
}
