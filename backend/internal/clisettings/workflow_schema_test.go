package clisettings

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWorkflowBooleanFileValidation(t *testing.T) {
	for _, raw := range []string{`{"enableWorkflows":true,"disableWorkflows":false,"future":{"x":1}}`, `{"enableWorkflows":false}`, `{"disableWorkflows":true}`, `{"workflowKeywordTriggerEnabled":true}`, `{"workflowKeywordTriggerEnabled":false,"future":{"keep":1}}`} {
		s, _, _ := testService(t, nil, nil)
		path := filepath.Join(s.options.ConfigHome, "workflow-original.json")
		if e := os.WriteFile(path, []byte(raw), 0666); e != nil {
			t.Fatal(e)
		}
		parsed, e := s.ParseFile(path)
		if e != nil || parsed.Settings == nil || len(parsed.Errors) != 0 {
			t.Fatal(raw, parsed, e)
		}
	}
	for _, raw := range []string{`{"enableWorkflows":null,"future":"keep"}`, `{"disableWorkflows":3,"model":"valid"}`, `{"disableWorkflows":"true"}`, `{"enableWorkflows":[],"disableWorkflows":{}}`, `{"workflowKeywordTriggerEnabled":null}`, `{"workflowKeywordTriggerEnabled":"false"}`, `{"workflowKeywordTriggerEnabled":[]}`} {
		s, _, _ := testService(t, nil, nil)
		path := filepath.Join(s.options.ConfigHome, "invalid-original.json")
		_ = os.WriteFile(path, []byte(raw), 0666)
		parsed, e := s.ParseFile(path)
		if e != nil || parsed.Settings != nil || len(parsed.Errors) == 0 {
			t.Fatal(raw, parsed, e)
		}
	}
}
