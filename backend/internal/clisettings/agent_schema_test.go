package clisettings

import (
	"testing"
)

func TestAgentFieldsUseSharedSchema(t *testing.T) {
	p, e := ValidateJSONContent(`{"builtInAgentOverrides":{"Explore":{"model":"  own-third-party-id  ","effort":"bad","future":{"keep":3}},"Plan":null},"strictPluginOnlyCustomization":["agents","future"],"futureRoot":{"keep":true}}`, "private settings")
	if e != nil {
		t.Fatal(e)
	}
	if p.Settings == nil || len(p.Errors) != 0 {
		t.Fatalf("settings=%+v", p)
	}
	if string(p.Settings.Get("builtInAgentOverrides")) != `{"Explore":{"model":"own-third-party-id","future":{"keep":3}},"Plan":{}}` {
		t.Fatalf("override=%s", p.Settings.Get("builtInAgentOverrides"))
	}
	if string(p.Settings.Get("strictPluginOnlyCustomization")) != `["agents"]` || string(p.Settings.Get("futureRoot")) != `{"keep":true}` {
		t.Fatalf("fields=%+v", p.Settings)
	}
}

func TestAgentSchemaCatchAndUnknownSiblingCompatibility(t *testing.T) {
	for _, input := range []string{`{"builtInAgentOverrides":null,"strictPluginOnlyCustomization":"agents"}`, `{"builtInAgentOverrides":42,"strictPluginOnlyCustomization":null}`} {
		p, e := ValidateJSONContent(input, "old settings")
		if e != nil || len(p.Errors) != 0 || p.Settings.Get("builtInAgentOverrides") != nil || p.Settings.Get("strictPluginOnlyCustomization") != nil {
			t.Fatalf("catch %s=%+v %v", input, p, e)
		}
	}
	input := `{"builtInAgentOverrides":{"one":{"model":42,"effort":-12,"future":"kept"},"two":{"model":"\ufeff  \ufeff","effort":" HIGH ","unknown":null},"three":{"model":"real","effort":"xhigh"},"four":false,"five":{"effort":2.5}},"strictPluginOnlyCustomization":[null,1,"mcp","hooks","skills","agents","mcp"]}`
	p, e := ValidateJSONContent(input, "old settings")
	if e != nil || len(p.Errors) != 0 {
		t.Fatalf("schema=%+v %v", p, e)
	}
	want := `{"one":{"effort":-12,"future":"kept"},"two":{"unknown":null},"three":{"model":"real","effort":"xhigh"},"four":{},"five":{}}`
	if string(p.Settings.Get("builtInAgentOverrides")) != want || string(p.Settings.Get("strictPluginOnlyCustomization")) != `["mcp","hooks","skills","agents","mcp"]` {
		t.Fatalf("parsed=%s policy=%s", p.Settings.Get("builtInAgentOverrides"), p.Settings.Get("strictPluginOnlyCustomization"))
	}
	for _, input := range []string{`{"strictPluginOnlyCustomization":true}`, `{"strictPluginOnlyCustomization":false}`} {
		p, e := ValidateJSONContent(input, "bool policy")
		if e != nil || p.Settings.Get("strictPluginOnlyCustomization") == nil {
			t.Fatal("boolean policy rejected")
		}
	}
}

func TestAgentModelSchemaRetainsLoneUTF16AndUnknownKeys(t *testing.T) {
	p, e := ValidateJSONContent(`{"builtInAgentOverrides":{"Explore":{"model":" \ud800 ","unknown\ud801":{"keep":true}}}}`, "old settings")
	if e != nil || len(p.Errors) != 0 || string(p.Settings.Get("builtInAgentOverrides")) != `{"Explore":{"model":"\ud800","unknown\ud801":{"keep":true}}}` {
		t.Fatalf("raw=%s error=%v", p.Settings.Get("builtInAgentOverrides"), e)
	}
}
