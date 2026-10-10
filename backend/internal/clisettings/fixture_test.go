package clisettings

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type stateOperation struct {
	Kind, Key, Ref, Value string
	Now, Window           float64
}
type stateCase struct {
	Name       string           `json:"name"`
	Operations []stateOperation `json:"operations"`
}
type stateRecord struct {
	Settings *string  `json:"settings"`
	Errors   []string `json:"errors"`
}
type stateInput struct {
	Home       string                 `json:"home"`
	Cases      []stateCase            `json:"cases"`
	Values     map[string]string      `json:"values"`
	Records    map[string]stateRecord `json:"records"`
	SourceKeys []string               `json:"sourceKeys"`
	PathKeys   []string               `json:"pathKeys"`
}
type stateObservation struct {
	Name     string `json:"name"`
	Steps    []any  `json:"steps"`
	NowCalls int    `json:"nowCalls"`
}
type stateView struct {
	Kind     string `json:"kind"`
	Identity string `json:"identity,omitempty"`
	Source   string `json:"source,omitempty"`
}
type keyedStateView struct {
	Key   string    `json:"key"`
	Value stateView `json:"value"`
}

func observeState(input stateInput, scenario stateCase) (stateObservation, error) {
	observation := stateObservation{Name: scenario.Name, Steps: []any{}}
	now := 1000.0
	state, _ := NewState(func() float64 { observation.NowCalls++; return now })
	values := map[string]*protocol.JSONObject{}
	for name, source := range input.Values {
		var value protocol.JSONObject
		if err := value.UnmarshalJSON([]byte(source)); err != nil {
			return observation, err
		}
		values[name] = &value
	}
	records := map[string]*ParsedSettings{}
	for name, original := range input.Records {
		record := &ParsedSettings{Errors: []json.RawMessage{}}
		if original.Settings != nil {
			record.Settings = values[*original.Settings]
		}
		for _, source := range original.Errors {
			record.Errors = append(record.Errors, json.RawMessage(source))
		}
		records[name] = record
	}
	valueView := func(value *protocol.JSONObject, exists bool) stateView {
		if !exists {
			return stateView{Kind: "undefined"}
		}
		if value == nil {
			return stateView{Kind: "null"}
		}
		raw, _ := value.MarshalJSON()
		view := stateView{Kind: "object", Source: string(protocol.StringifyValidatedJavaScriptJSON(raw))}
		for name, original := range values {
			if value == original {
				view.Identity = name
			}
		}
		return view
	}
	recordView := func(value *ParsedSettings, null bool) stateView {
		if value == nil {
			if null {
				return stateView{Kind: "null"}
			}
			return stateView{Kind: "undefined"}
		}
		settings := json.RawMessage("null")
		if value.Settings != nil {
			settings, _ = value.Settings.MarshalJSON()
		}
		errors, _ := json.Marshal(value.Errors)
		object := protocol.NewJSONObject(protocol.JSONField{Name: "settings", Value: settings}, protocol.JSONField{Name: "errors", Value: errors})
		raw, _ := object.MarshalJSON()
		view := stateView{Kind: "object", Source: string(protocol.StringifyValidatedJavaScriptJSON(raw))}
		for name, original := range records {
			if value == original {
				view.Identity = name
			}
		}
		return view
	}
	for _, operation := range scenario.Operations {
		switch operation.Kind {
		case "time":
			now = operation.Now
		case "setSession":
			state.SetSessionSettingsCache(records[operation.Ref])
		case "setSource":
			state.SetCachedSettingsForSource(operation.Key, values[operation.Ref])
		case "setParsed":
			state.SetCachedParsedFile(operation.Key, records[operation.Ref])
		case "setPlugin":
			state.SetPluginSettingsBase(values[operation.Ref])
		case "clearPlugin":
			state.ClearPluginSettingsBase()
		case "reset":
			state.ResetSettingsCache()
		case "mutateValue":
			values[operation.Ref].Set(operation.Key, json.RawMessage(operation.Value))
		case "mutateErrors":
			records[operation.Ref].Errors = append(records[operation.Ref].Errors, json.RawMessage(operation.Value))
		case "mark":
			state.MarkInternalWrite(operation.Key)
		case "consume":
			observation.Steps = append(observation.Steps, struct {
				Kind   string `json:"kind"`
				Key    string `json:"key"`
				Result bool   `json:"result"`
			}{"consume", operation.Key, state.ConsumeInternalWrite(operation.Key, operation.Window)})
		case "clearWrites":
			state.ClearInternalWrites()
		case "snapshot":
			sources, paths := []keyedStateView{}, []keyedStateView{}
			for _, key := range input.SourceKeys {
				value, exists := state.GetCachedSettingsForSource(key)
				sources = append(sources, keyedStateView{key, valueView(value, exists)})
			}
			for _, key := range input.PathKeys {
				paths = append(paths, keyedStateView{key, recordView(state.GetCachedParsedFile(key), false)})
			}
			observation.Steps = append(observation.Steps, struct {
				Kind    string           `json:"kind"`
				Session stateView        `json:"session"`
				Source  []keyedStateView `json:"source"`
				Parsed  []keyedStateView `json:"parsed"`
				Plugin  stateView        `json:"plugin"`
			}{"snapshot", recordView(state.GetSessionSettingsCache(), true), sources, paths, valueView(state.GetPluginSettingsBase(), state.GetPluginSettingsBase() != nil)})
		default:
			return observation, fmt.Errorf("unknown CLI settings state operation %q", operation.Kind)
		}
	}
	return observation, nil
}

func TestSettingsStateFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_SETTINGS_STATE_FIXTURE") != "1" {
		t.Skip("private settings state fixture only")
	}
	raw, err := io.ReadAll(os.Stdin)
	if err != nil {
		t.Fatal(err)
	}
	var input stateInput
	if err := json.Unmarshal(raw, &input); err != nil {
		t.Fatal(err)
	}
	home, err := filepath.EvalSymlinks(input.Home)
	root, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	if err != nil || rootErr != nil || home != cwd || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") || !strings.HasPrefix(home, root+string(filepath.Separator)) || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		t.Fatal("CLI settings state requires disposable HOME/config before execution")
	}
	result := struct {
		Observations []stateObservation `json:"observations"`
	}{Observations: []stateObservation{}}
	for _, scenario := range input.Cases {
		observed, err := observeState(input, scenario)
		if err != nil {
			t.Fatal(err)
		}
		result.Observations = append(result.Observations, observed)
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println("SETTINGS_STATE_RESULT:" + string(encoded))
}
