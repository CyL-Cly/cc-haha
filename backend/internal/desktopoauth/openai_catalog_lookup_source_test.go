package desktopoauth

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func TestOpenAIRemoteCatalogLookupPreservesActualSourceUnicodeAndRawIdentity(t *testing.T) {
	rows := []map[string]any{}
	for _, entry := range []string{`"mixed-MODEL"`, `" padded "`, `"İ"`, `"ΟΣ"`, `"𐐀"`, `"\ud800"`, `"\ufffd"`, `"model\udfff"`} {
		catalog := json.RawMessage(`[{"value":` + entry + `,"label":"Owned model","description":"","defaultReasoningEffort":"low","supportedReasoningEfforts":["low"]}]`)
		for _, model := range []string{"mixed-model", " PADDED ", "İ", "i\u0307", "ΟΣ", "ος", "οσ", "𐐀", "𐐨", "\ufffd", "model\ufffd"} {
			rows = append(rows, map[string]any{"catalog": catalog, "model": model})
		}
	}
	var source struct{ Lookups []json.RawMessage }
	_ = json.Unmarshal(actualOpenAICatalogSource(t, map[string]any{"lookups": rows}), &source)
	if len(source.Lookups) != len(rows) {
		t.Fatal("Source lookup count")
	}
	for i, row := range rows {
		var catalog []providerproxy.OpenAIModel
		if err := json.Unmarshal(row["catalog"].(json.RawMessage), &catalog); err != nil {
			t.Fatal(err)
		}
		got := providerproxy.OpenAIModelCatalogEntry(row["model"].(string), catalog)
		if !bytes.Equal(encode(got), source.Lookups[i]) {
			t.Errorf("row%d lookup%s want%s", i, encode(got), source.Lookups[i])
		}
		if got != nil {
			got.Raw[0] = '!'
			if !json.Valid(catalog[0].Raw) {
				t.Fatal("lookup caller mutated original catalog")
			}
		}
	}
}
