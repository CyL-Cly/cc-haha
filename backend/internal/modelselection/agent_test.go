package modelselection

import (
	"bytes"
	"encoding/json"
	"os"
	"slices"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func TestAgentAndRuntimeModelsMatchActualSource(t *testing.T) {
	data, err := os.ReadFile("testdata/actual_agent_source.json")
	if err != nil {
		t.Fatal(err)
	}
	type runtimeInput struct {
		Parent            json.RawMessage
		Mode              string
		Exceeds200kTokens bool
	}
	type agentInput struct {
		AgentModel, Parent, ToolModel json.RawMessage
		Mode                          string
	}
	var corpus struct {
		Rows []struct {
			Scenario struct {
				Name     string
				Env      map[string]string
				Settings protocol.JSONObject
				Override json.RawMessage
			}
			Observation struct {
				SpecifiedJSON *string
				Runtime       []struct {
					Input     runtimeInput
					ValueJSON string
				}
				Agents []struct {
					Input     agentInput
					ValueJSON string
				}
			}
		}
		SourceOnly struct {
			Consumer, Runtime string
			Capabilities      struct{ Blocked []string }
		}
	}
	if err = json.Unmarshal(data, &corpus); err != nil || len(corpus.Rows) != 17 {
		t.Fatalf("actual Source corpus rows=%d err=%v", len(corpus.Rows), err)
	}
	if corpus.SourceOnly.Consumer != "src/utils/model/model.ts + modelAllowlist.ts" || len(corpus.SourceOnly.Capabilities.Blocked) != 0 {
		t.Fatal("missing guarded actual Source evidence")
	}
	assertModel := func(got json.RawMessage, err error, want string) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
		if bytes.Equal(got, []byte(want)) {
			return
		}
		g, ge := units(got)
		w, we := units([]byte(want))
		if ge != nil || we != nil || !slices.Equal(g, w) {
			t.Fatalf("model got %s want %s", got, want)
		}
	}
	var runtimeCount, agentCount int
	for _, row := range corpus.Rows {
		t.Run(row.Scenario.Name, func(t *testing.T) {
			env := map[string]string{"ANTHROPIC_API_KEY": "fake-model-selection-no-network", "ANTHROPIC_BASE_URL": "http://127.0.0.1:43210"}
			for key, value := range row.Scenario.Env {
				env[key] = value
			}
			s, err := NewDirectCredentials(Options{Environment: env, Settings: row.Scenario.Settings, Override: row.Scenario.Override})
			if err != nil {
				t.Fatal(err)
			}
			specified, err := s.UserSpecified()
			if row.Observation.SpecifiedJSON == nil {
				if err != nil || specified != nil {
					t.Fatalf("undefined specified=%s err=%v", specified, err)
				}
			} else {
				assertModel(specified, err, *row.Observation.SpecifiedJSON)
			}
			for _, record := range row.Observation.Runtime {
				got, err := s.RuntimeModel(record.Input.Parent, record.Input.Mode, record.Input.Exceeds200kTokens)
				assertModel(got, err, record.ValueJSON)
				runtimeCount++
			}
			for _, record := range row.Observation.Agents {
				got, err := s.AgentModel(AgentInput{AgentModel: record.Input.AgentModel, ParentModel: record.Input.Parent, ToolSpecifiedModel: record.Input.ToolModel, PermissionMode: record.Input.Mode})
				assertModel(got, err, record.ValueJSON)
				agentCount++
			}
		})
	}
	if runtimeCount != 64 || agentCount != 1365 {
		t.Fatalf("incomplete actual Source observations runtime=%d agents=%d", runtimeCount, agentCount)
	}
}
