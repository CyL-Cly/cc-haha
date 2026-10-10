package modelaccounting

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestNativeCalculatorMatchesActualSourcePricingAndLedger(t *testing.T) {
	data, err := os.ReadFile("testdata/actual_native_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Rows []struct {
			Scenario struct {
				Name     string
				Env      map[string]string
				Settings protocol.JSONObject
			}
			Observation struct {
				Accounting []struct {
					Input struct {
						Model string
						Usage protocol.JSONObject
					}
					Cost, Total    float64
					ModelUsageJSON string
					Unknown        bool
				}
			}
		}
		SourceOnly struct{ Capabilities struct{ Blocked []string } }
	}
	if err = json.Unmarshal(data, &corpus); err != nil || len(corpus.Rows) != 4 {
		t.Fatal("invalid actual Source corpus", err, len(corpus.Rows))
	}
	if len(corpus.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal("Source used unavailable external capability")
	}
	count := 0
	for _, row := range corpus.Rows {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			env := map[string]string{"ANTHROPIC_API_KEY": "fake-model-selection-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210"}
			for k, v := range row.Scenario.Env {
				env[k] = v
			}
			models := protocol.NewJSONObject()
			total := float64(0)
			unknown := false
			for _, record := range row.Observation.Accounting {
				a, err := NewNativeCalculator(record.Input.Model, NativeCalculatorOptions{Environment: env, Settings: func() (*protocol.JSONObject, error) { copy := row.Scenario.Settings.Clone(); return &copy, nil }, Global: func() (*protocol.JSONObject, error) { empty := protocol.NewJSONObject(); return &empty, nil }, SDKBetas: []string{}})
				if err != nil {
					t.Fatal(err)
				}
				a.Total, a.Models = total, models.Clone()
				cost, err := a.Charge(record.Input.Usage)
				if err != nil || cost != record.Cost || a.Total != record.Total {
					t.Fatalf("%s cost=%.17g want=%.17g total=%.17g want=%.17g err=%v", record.Input.Model, cost, record.Cost, a.Total, record.Total, err)
				}
				unknown = unknown || a.UnknownModel
				if unknown != record.Unknown {
					t.Fatalf("%s unknown=%v want=%v", record.Input.Model, unknown, record.Unknown)
				}
				encoded, err := a.Models.MarshalJSON()
				if err != nil {
					t.Fatal(err)
				}
				var got, want any
				if err = json.Unmarshal(encoded, &got); err != nil {
					t.Fatal(err)
				}
				if err = json.Unmarshal([]byte(record.ModelUsageJSON), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("%s actual ledger=%s want=%s", record.Input.Model, encoded, record.ModelUsageJSON)
				}
				total, models = a.Total, a.Models.Clone()
				count++
			}
		})
	}
	if count != 216 {
		t.Fatalf("actual Source observations=%d", count)
	}
}
