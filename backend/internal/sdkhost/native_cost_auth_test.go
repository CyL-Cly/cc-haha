package sdkhost

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
)

func TestNativeCostAuthUsesActualSourceBearerAndAPIKeyShortCircuit(t *testing.T) {
	bytes, err := os.ReadFile("testdata/native_cost_auth_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Observations []struct {
			Scenario struct {
				Name        string
				Environment map[string]string
				Supported   bool
			}
			Enabled, Subscriber, FormatsCost, SubscriptionMessage bool
			ResultType                                            string
		}
		SourceOnly struct {
			Consumer     string
			Capabilities struct{ Blocked []string }
		}
	}
	if err := json.Unmarshal(bytes, &corpus); err != nil {
		t.Fatal(err)
	}
	if len(corpus.Observations) != 26 || len(corpus.SourceOnly.Capabilities.Blocked) > 0 || !strings.Contains(corpus.SourceOnly.Consumer, "actual src/utils/auth") {
		t.Fatal("missing real Source auth/cost capture")
	}
	for _, row := range corpus.Observations {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			subscriber, err := nativeCostSubscriber(row.Scenario.Environment)
			if !row.Scenario.Supported {
				var capability *CapabilityError
				if !errors.As(err, &capability) {
					t.Fatalf("unported OAuth domain silently treated as nonsubscriber: %v", err)
				}
				return
			}
			if err != nil || subscriber != row.Subscriber || row.Enabled || !row.FormatsCost || row.SubscriptionMessage || row.ResultType != "text" {
				t.Fatalf("actual Source direct auth/cost changed: %#v, subscriber=%v error=%v", row, subscriber, err)
			}
		})
	}
}
