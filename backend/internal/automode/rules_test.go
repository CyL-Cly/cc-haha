package automode

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
)

func TestTrustedRulesReplaceExpandAndRejectWholeSource(t *testing.T) {
	snapshot := Snapshot{TranscriptClassifier: true, Sources: map[string]json.RawMessage{
		"userSettings":    json.RawMessage(`{"autoMode":{"allow":["before","$defaults","after"],"soft_deny":[],"environment":["user"]}}`),
		"projectSettings": json.RawMessage(`{"autoMode":{"allow":["untrusted"]}}`),
		"localSettings":   json.RawMessage(`{"autoMode":{"allow":["local"],"environment":[null]}}`),
		"flagSettings":    json.RawMessage(`{"autoMode":{"allow":["flag"],"hard_deny":["\\uD800", "\ud800"],"classifyAllShell":true}}`),
		"policySettings":  json.RawMessage(`{"autoMode":{"allow":["policy"],"classifyAllShell":false}}`),
	}}
	config := MergeTrusted(snapshot)
	if config == nil || len(config.Allow) != 5 || len(config.SoftDeny) != 0 || config.SoftDeny == nil || config.ClassifyAllShell == nil || *config.ClassifyAllShell {
		t.Fatalf("incorrect trusted merge: %+v", config)
	}
	rules := Effective(snapshot)
	if len(rules.Allow) != len(Defaults(true).Allow)+4 || string(rules.Allow[0]) != `"before"` || string(rules.Allow[len(rules.Allow)-1]) != `"policy"` || len(rules.SoftDeny) != 0 || string(rules.Environment[0]) != `"user"` {
		t.Fatalf("incorrect replacement/default expansion: %+v", rules)
	}
	var out bytes.Buffer
	if err := WriteRules(&out, rules); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), `"\ud800"`) || strings.Contains(out.String(), `\ufffd`) || strings.Contains(out.String(), "untrusted") || strings.Contains(out.String(), "local") || !strings.HasSuffix(out.String(), "\n") {
		t.Fatalf("lost source identity/trust: %s", out.String())
	}
	config.Allow[0][1] = 'X'
	if string(rules.Allow[0]) != `"before"` {
		t.Fatal("effective rules alias merged config")
	}
}

func TestFeatureAndSchemaBoundaries(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"allow":null}`, `{"allow":[3]}`, `{"allow":{},"environment":[]}`, `{"classifyAllShell":"true"}`} {
		snapshot := Snapshot{TranscriptClassifier: true, Sources: map[string]json.RawMessage{"userSettings": json.RawMessage(`{"autoMode":` + raw + `}`)}}
		if MergeTrusted(snapshot) != nil {
			t.Fatalf("invalid source accepted: %s", raw)
		}
	}
	snapshot := Snapshot{TranscriptClassifier: true, Sources: map[string]json.RawMessage{"userSettings": json.RawMessage(`{"autoMode":{"deny":["legacy"]}}`)}}
	if MergeTrusted(snapshot) != nil {
		t.Fatal("external build accepted internal-only deny")
	}
	snapshot.AnthropicInternal = true
	if config := MergeTrusted(snapshot); config == nil || string(config.SoftDeny[0]) != `"legacy"` {
		t.Fatalf("missing internal deny: %+v", config)
	}
	snapshot.TranscriptClassifier = false
	if MergeTrusted(snapshot) != nil || len(Effective(snapshot).Allow) != 0 {
		t.Fatal("disabled build exposed classifier rules")
	}
}

func TestOriginalTemplateRecipeAndFormatting(t *testing.T) {
	values := extract("<x>\n\ufeff - one\u2028\n- two\v\n* ignored\n- \n- three\n</x><x>\n- never\n</x>", "x")
	if len(values) != 3 || string(values[0]) != `"one"` || string(values[1]) != `"two"` || string(values[2]) != `"three"` {
		t.Fatalf("incorrect bullet extraction: %q", values)
	}
	if values := extract("unmatched", "x"); len(values) != 0 {
		t.Fatalf("invented missing section: %q", values)
	}
	rules := emptyRules()
	rules.Allow = []json.RawMessage{json.RawMessage(`"<>&\u2028\ud800"`)}
	var out bytes.Buffer
	if err := WriteRules(&out, rules); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "<>&") || !strings.Contains(out.String(), "\u2028") || strings.Contains(out.String(), `\u003c`) {
		t.Fatalf("changed JSON.stringify quoting: %s", out.String())
	}
	if err := WriteRules(&out, Rules{Allow: []json.RawMessage{json.RawMessage(`invalid`)}}); err == nil {
		t.Fatal("invalid public raw rule should fail before writing")
	}
	if err := DefaultsHandler(errorWriter{}, true); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("lost writer failure: %v", err)
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }
