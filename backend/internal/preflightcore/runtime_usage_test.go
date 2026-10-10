package preflightcore

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeRuntimeUsageMatchesActualSourceLatestAssistant(t *testing.T) {
	data, err := os.ReadFile("testdata/native_runtime_usage_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Observations []struct {
			Scenario struct {
				Name    string
				History []protocol.JSONObject
			}
			Observation bool
		}
		SourceOnly struct {
			Consumer     string
			Capabilities struct{ Blocked []string }
		}
	}
	if err = json.Unmarshal(data, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Observations) != 24 || len(corpus.SourceOnly.Capabilities.Blocked) != 0 || !strings.Contains(corpus.SourceOnly.Consumer, "doesMostRecentAssistantMessageExceed200k") {
		t.Fatal("missing actual Source runtime observation")
	}
	for _, row := range corpus.Observations {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			got, err := DoesMostRecentAssistantMessageExceed200k(row.Scenario.History)
			if err != nil || got != row.Observation {
				t.Fatalf("latest assistant policy mismatch: got %v/%v, want %v", got, err, row.Observation)
			}
		})
	}
}
