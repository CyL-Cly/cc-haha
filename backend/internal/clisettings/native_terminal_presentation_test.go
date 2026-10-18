package clisettings

import "testing"

func TestNativeTerminalPresentationBooleanSchemaKeepsOldAndUnknownFields(t *testing.T) {
	for _, key := range []string{"spinnerTipsEnabled", "prefersReducedMotion", "syntaxHighlightingDisabled"} {
		for _, value := range []string{"true", "false"} {
			input := `{"` + key + `":` + value + `,"futurePresentation":{"preserve":23}}`
			parsed, err := ValidateNativeJSONContent(input, "fixture")
			if err != nil || parsed.Settings == nil || len(parsed.Errors) != 0 || string(parsed.Settings.Get(key)) != value || string(parsed.Settings.Get("futurePresentation")) != `{"preserve":23}` {
				t.Fatal(input, err, parsed)
			}
			if _, err = ValidateJSONContent(input, "finite"); err == nil {
				t.Fatal("finite profile broadened", key)
			}
		}
		for _, value := range []string{"null", `"false"`, "0", "{}", "[]"} {
			parsed, err := ValidateNativeJSONContent(`{"`+key+`":`+value+`}`, "fixture")
			if err != nil || parsed.Settings != nil || len(parsed.Errors) != 1 {
				t.Fatal(key, value, err, parsed)
			}
		}
	}
}
