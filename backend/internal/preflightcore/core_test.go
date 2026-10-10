package preflightcore

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"os"
	"strings"
	"testing"
)

type sourceOriginal struct {
	Name, Kind                 string
	HistorySources             []string
	ContentSource, ModelSource string
	Env                        map[string]string
	Repeat                     int
}
type sourceNumber struct {
	Kind, Raw string
	Value     *float64
}
type sourceOutcome struct {
	Output struct {
		Count                 sourceNumber
		HasUsableContextUsage bool
		Index                 int
	}
	Error                *SourceError
	View                 []string
	ViewSameArray        *bool
	ViewReferenceIndices []int
	Initial, After       []string
}

func originalObjects(raw []string) ([]protocol.JSONObject, error) {
	result := make([]protocol.JSONObject, 0, len(raw))
	for _, value := range raw {
		var record protocol.JSONObject
		if err := json.Unmarshal([]byte(value), &record); err != nil {
			return nil, err
		}
		result = append(result, record)
	}
	return result, nil
}
func mustOriginal(t *testing.T, raw ...string) []protocol.JSONObject {
	t.Helper()
	records, err := originalObjects(raw)
	if err != nil {
		t.Fatal(err)
	}
	return records
}
func finalStrings(t *testing.T, records []protocol.JSONObject) []string {
	t.Helper()
	values := make([]string, 0, len(records))
	for _, r := range records {
		raw, err := r.MarshalJSON()
		if err != nil {
			t.Fatal(err)
		}
		raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, string(raw))
	}
	return values
}
func compareCount(t *testing.T, actual float64, want sourceNumber) {
	t.Helper()
	if want.Kind == "NaN" {
		if !math.IsNaN(actual) {
			t.Fatal("original malformed numeric text did not retain NaN")
		}
		return
	}
	if want.Value == nil || actual != *want.Value {
		t.Fatalf("actual Source count mismatch %v vs %+v", actual, want)
	}
}
func compareProjection(t *testing.T, actual Projection, want sourceOutcome) {
	t.Helper()
	if want.ViewSameArray == nil || actual.SameInputArray != *want.ViewSameArray {
		t.Fatal("original array identity changed")
	}
	if len(actual.Records) != len(want.View) {
		t.Fatal("view record count differs")
	}
	for index, r := range actual.Records {
		got := finalStrings(t, []protocol.JSONObject{r.Record})[0]
		if got != want.View[index] {
			t.Fatalf("model-visible record differs\n%s\n%s", got, want.View[index])
		}
		reference := r.OriginalIndex
		if r.EnvelopeChanged {
			reference = -1
		}
		if reference != want.ViewReferenceIndices[index] {
			t.Fatal("reference/provenance conflated")
		}
		if r.EnvelopeChanged != r.MessageChanged {
			t.Fatal("clone metadata lost nested-message change")
		}
	}
}

func TestOriginalSourcePublicCore(t *testing.T) {
	raw, err := os.ReadFile("testdata/source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract struct {
		Rows []struct {
			Name         string
			OriginalCase sourceOriginal
			Outcome      []sourceOutcome
		}
	}
	if err = json.Unmarshal(raw, &contract); err != nil {
		t.Fatal(err)
	}
	if len(contract.Rows) != 73 {
		t.Fatal("not the frozen73 Source original cases")
	}
	for _, row := range contract.Rows {
		t.Run(row.Name, func(t *testing.T) {
			input := row.OriginalCase
			original, err := originalObjects(input.HistorySources)
			if err != nil {
				t.Fatal(err)
			}
			before := finalStrings(t, original)
			model := ModelInput{Original: []byte(input.ModelSource), Environment: input.Env}
			want := row.Outcome[0]
			var count float64
			var usable bool
			var result Projection
			var index int
			switch input.Kind {
			case "rough-content":
				count, err = RoughContent([]byte(input.ContentSource))
			case "rough-history":
				count, err = RoughHistory(original)
			case "tokens":
				count, err = EstimateContext(original)
				if err == nil {
					usable, err = HasUsableContextUsage(original)
				}
			case "boundary":
				result, err = AfterCompactBoundary(original)
				if err == nil {
					index, err = LastCompactBoundaryIndex(original)
				}
			case "signature":
				result, err = StripProtectedSignatures(original, model)
			case "boundary-signature-tokens":
				var boundary Projection
				boundary, err = AfterCompactBoundary(original)
				if err == nil {
					records := make([]protocol.JSONObject, 0, len(boundary.Records))
					for _, r := range boundary.Records {
						records = append(records, r.Record)
					}
					result, err = StripProtectedSignatures(records, model)
					if err == nil {
						for i := range result.Records {
							result.Records[i].OriginalIndex = boundary.Records[result.Records[i].OriginalIndex].OriginalIndex
						}
						result.SameInputArray = result.SameInputArray && boundary.SameInputArray
						records = nil
						for _, r := range result.Records {
							records = append(records, r.Record)
						}
						count, err = EstimateContext(records)
						if err == nil {
							index, err = LastCompactBoundaryIndex(original)
						}
					}
				}
			default:
				t.Fatal("unknown frozen kind")
			}
			if err != nil {
				var capability *CapabilityError
				var source *SourceError
				if errors.As(err, &capability) {
					allowed := strings.Contains(capability.Boundary, "attachment") || strings.Contains(capability.Boundary, "alias") || strings.Contains(capability.Boundary, "scalar")
					if !allowed {
						t.Fatal("unexpected original boundary", err)
					}
					if capability.Error() == "" {
						t.Fatal("missing capability diagnostics")
					}
					return
				}
				if !errors.As(err, &source) || want.Error == nil || source.Message != want.Error.Message || source.Class != want.Error.Class || source.Name != want.Error.Name || source.MessageJSON != want.Error.MessageJSON {
					t.Fatal("Source semantic error mismatch", err)
				}
				if source.Error() != want.Error.Name+": "+want.Error.Message {
					t.Fatal("error lost class/message")
				}
				return
			}
			if want.Error != nil {
				t.Fatal("Source error silently accepted")
			}
			switch input.Kind {
			case "rough-content", "rough-history", "tokens", "boundary-signature-tokens":
				compareCount(t, count, want.Output.Count)
			}
			if input.Kind == "tokens" && usable != want.Output.HasUsableContextUsage {
				t.Fatal("usable usage decision mismatch")
			}
			if input.Kind == "boundary" || input.Kind == "boundary-signature-tokens" {
				if index != want.Output.Index {
					t.Fatal("compact marker mismatch")
				}
			}
			if input.Kind == "boundary" || input.Kind == "signature" || input.Kind == "boundary-signature-tokens" {
				compareProjection(t, result, want)
			}
			after := finalStrings(t, original)
			if strings.Join(before, "\x00") != strings.Join(after, "\x00") {
				t.Fatal("input changed")
			}
			for i := range after {
				if after[i] != want.After[i] {
					t.Fatal("Source original record bytes mutated")
				}
			}
		})
	}
}

func TestJSONObjectValueSharingAndProjectionOwnership(t *testing.T) {
	t.Run("same-model-shares-record-state", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"assistant","uuid":"a","message":{"model":"fixture-model","content":[{"type":"thinking","thinking":"secret","signature":"s"},{"type":"text","text":"live"}]}}`)
		view, err := StripProtectedSignatures(original, ModelInput{Original: []byte(`"fixture-model"`)})
		if err != nil {
			t.Fatal(err)
		}
		if !view.SameInputArray || view.Records[0].OriginalIndex != 0 || view.Records[0].EnvelopeChanged || view.Records[0].MessageChanged {
			t.Fatal("unchanged metadata fabricated clone")
		}
		copy := view.Records[0].Record
		copy.Set("sharing_probe", []byte(`true`))
		if !bytes.Equal(original[0].Get("sharing_probe"), []byte(`true`)) {
			t.Fatal("JSONObject value copy lost caller state sharing")
		}
	})
	t.Run("changed-model-clone-keeps-ancestry-not-sharing", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"assistant","future":"\ud800","uuid":"a","message":{"id":"r","model":"old","content":[{"type":"thinking","thinking":"secret","signature":"s"},{"type":"text","text":"\udc00"}]}}`, `{"type":"user","uuid":"u","message":{"role":"user","content":"next"}}`)
		before := finalStrings(t, original)
		view, err := StripProtectedSignatures(original, ModelInput{Original: []byte(`"deepseek-flash"`)})
		if err != nil {
			t.Fatal(err)
		}
		if view.SameInputArray || !view.Records[0].EnvelopeChanged || !view.Records[0].MessageChanged || view.Records[0].OriginalIndex != 0 || view.Records[1].OriginalIndex != 1 || view.Records[1].EnvelopeChanged {
			t.Fatal("changed/unchanged ancestry metadata lost")
		}
		clone := view.Records[0].Record
		clone.Set("clone_probe", []byte(`true`))
		if original[0].Get("clone_probe") != nil {
			t.Fatal("changed outer record still aliases original")
		}
		var api protocol.JSONObject
		if err = json.Unmarshal(clone.Get("message"), &api); err != nil {
			t.Fatal(err)
		}
		api.Set("clone_nested_probe", []byte(`true`))
		raw, _ := api.MarshalJSON()
		clone.Set("message", raw)
		after := finalStrings(t, original)
		for i := range before {
			if before[i] != after[i] {
				t.Fatal("projection changed original raw message bytes")
			}
		}
		unchanged := view.Records[1].Record
		unchanged.Set("sharing_probe", []byte(`true`))
		if original[1].Get("sharing_probe") == nil {
			t.Fatal("unaffected record incorrectly cloned")
		}
	})
	t.Run("inclusive-compact-retains-state-and-origin", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"user","message":{"content":"old"}}`, `{"type":"system","subtype":"compact_boundary"}`, `{"type":"user","message":{"content":"new"}}`)
		view, err := AfterCompactBoundary(original)
		if err != nil {
			t.Fatal(err)
		}
		if view.SameInputArray || len(view.Records) != 2 || view.Records[0].OriginalIndex != 1 || view.Records[1].OriginalIndex != 2 || view.Records[0].EnvelopeChanged || view.Records[1].MessageChanged {
			t.Fatal("compact copied/removed marker or lost ancestry")
		}
		copy := view.Records[1].Record
		copy.Set("shared", []byte(`1`))
		if original[2].Get("shared") == nil {
			t.Fatal("compact cloned retained record")
		}
	})
}

func TestNullUsageHasSeparateObservableCallBoundary(t *testing.T) {
	original := mustOriginal(t, `{"type":"assistant","message":{"model":"fixture-model","content":[{"type":"text","text":"x"}],"usage":null}}`)
	count, err := EstimateContext(original)
	if err != nil || count != 0 {
		t.Fatal("estimate evaluated hasUsable's null property error", count, err)
	}
	ok, err := HasUsableContextUsage(original)
	var source *SourceError
	if ok || !errors.As(err, &source) || source.Message != "null is not an object (evaluating 'usage.input_tokens')" {
		t.Fatal("hasUsable lost separately captured Source error", ok, err)
	}
}

func TestPublicTypedCapabilitiesNeverSupplyProjection(t *testing.T) {
	protected := mustOriginal(t, `{"type":"assistant","message":{"model":"fixture-model","content":[{"type":"thinking","thinking":"s","signature":"bound"}]}}`)
	t.Run("unknown-env", func(t *testing.T) {
		view, err := StripProtectedSignatures(protected, ModelInput{Original: []byte(`"fixture-model"`), Environment: map[string]string{"ANTHROPIC_BASE_URL": "https://example.invalid"}})
		assertCap(t, err)
		if len(view.Records) != 0 {
			t.Fatal("cap supplied records")
		}
	})
	t.Run("invalid-model", func(t *testing.T) {
		_, err := StripProtectedSignatures(protected, ModelInput{Original: []byte(`"bad\q"`)})
		assertCap(t, err)
	})
	t.Run("undefined-current-model", func(t *testing.T) { _, err := StripProtectedSignatures(protected, ModelInput{}); assertCap(t, err) })
	t.Run("nonascii-model", func(t *testing.T) {
		_, err := StripProtectedSignatures(protected, ModelInput{Original: []byte(`"模型"`)})
		assertCap(t, err)
	})
	t.Run("invalid-content", func(t *testing.T) { _, err := RoughContent([]byte(`"bad\q"`)); assertCap(t, err) })
	t.Run("invalid-ordered-field", func(t *testing.T) {
		r := protocol.NewJSONObject()
		r.Set("message", []byte(`{"bad":`))
		_, err := EstimateContext([]protocol.JSONObject{r})
		assertCap(t, err)
	})
	for _, name := range []string{"last", "after", "signatures", "rough", "estimate", "usable"} {
		t.Run("scalar-"+name, func(t *testing.T) {
			records := []protocol.JSONObject{{}}
			var err error
			switch name {
			case "last":
				_, err = LastCompactBoundaryIndex(records)
			case "after":
				_, err = AfterCompactBoundary(records)
			case "signatures":
				_, err = StripProtectedSignatures(records, ModelInput{Original: []byte(`"fixture-model"`)})
			case "rough":
				_, err = RoughHistory(records)
			case "estimate":
				_, err = EstimateContext(records)
			case "usable":
				_, err = HasUsableContextUsage(records)
			}
			assertCap(t, err)
		})
	}
	t.Run("retained-attachment-before-anchor", func(t *testing.T) {
		records := mustOriginal(t, `{"type":"attachment","attachment":{"type":"skill_listing","content":"original"}}`, `{"type":"assistant","message":{"model":"fixture-model","id":"r","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":20,"output_tokens":1}}}`)
		count, err := EstimateContext(records)
		if err != nil || count != 21 {
			t.Fatal("Source does not project attachments before a usable usage anchor", count, err)
		}
	})
	t.Run("infinite-usage-sum", func(t *testing.T) {
		records := mustOriginal(t, `{"type":"assistant","message":{"model":"fixture-model","id":"r","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":1e308,"output_tokens":1e308}}}`)
		_, err := EstimateContext(records)
		assertCap(t, err)
	})
	t.Run("undefined-content-is-real-zero", func(t *testing.T) {
		count, err := RoughContent(nil)
		if err != nil || count != 0 {
			t.Fatal("undefined content changed", count, err)
		}
	})
}
func assertCap(t *testing.T, err error) {
	t.Helper()
	var cap *CapabilityError
	if !errors.As(err, &cap) || cap.Boundary == "" || cap.Error() == "" {
		t.Fatal("not a usable typed capability", err)
	}
}

// These controls cover native-only rejection/decoder responsibilities. They
// do not add Source-compatible positive profiles to the original73 corpus.
func TestPublicUnprovedEstimatorBranches(t *testing.T) {
	for _, row := range []struct{ name, content string }{
		{"noniterable", `{}`}, {"unproved-tool-name", `[{"type":"tool_use","name":null,"input":{}}]`},
		{"null-redacted-data", `[{"type":"redacted_thinking","data":null}]`}, {"null-thinking-length", `[{"type":"thinking","thinking":null}]`},
	} {
		t.Run(row.name, func(t *testing.T) { _, err := RoughContent([]byte(row.content)); assertCap(t, err) })
	}
	t.Run("rough-history-real-count-and-error-propagation", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"user","message":{"content":"😀aa"}}`, `{"type":"assistant","message":{"model":"fixture-model","content":[{"type":"text","text":"abcde"}]}}`)
		rough, err := RoughHistory(original)
		if err != nil {
			t.Fatal(err)
		}
		context, err := EstimateContext(original)
		if err != nil || rough != context {
			t.Fatal("no-anchor context estimate did not use the same independent history rough count")
		}
		malformed := mustOriginal(t, `{"type":"user","message":{"content":123}}`)
		_, err = RoughHistory(malformed)
		var source *SourceError
		if !errors.As(err, &source) || source.Message != "number is not iterable" {
			t.Fatal("rough history hid the captured rough-content error", err)
		}
	})
	t.Run("nonstring-response-id", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"assistant","message":{"id":123,"model":"fixture-model","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":20,"output_tokens":1}}}`)
		_, err := EstimateContext(original)
		assertCap(t, err)
	})
	t.Run("synthetic-text-not-usage-anchor", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"assistant","message":{"id":"r","model":"fixture-model","content":[{"type":"text","text":"[Request interrupted by user]"}],"usage":{"input_tokens":2000,"output_tokens":1}}}`)
		ok, err := HasUsableContextUsage(original)
		if err != nil || ok {
			t.Fatal("synthetic display text became a context usage anchor", ok, err)
		}
	})
}

func TestCoreInternalMalformedDecoderGuards(t *testing.T) {
	t.Run("invalid-string-escape", func(t *testing.T) {
		if _, err := roughString([]byte(`"bad\uZ000"`)); err == nil {
			t.Fatal("invalid UTF-16 JSON produced a count")
		}
	})
	t.Run("incomplete-content-array", func(t *testing.T) {
		if _, err := roughContent([]byte(`[{"type":"text"}`)); err == nil {
			t.Fatal("incomplete content array produced a count")
		}
	})
	t.Run("invalid-catchall-json", func(t *testing.T) {
		if _, err := roughBlock([]byte(`{"incomplete":`)); err == nil {
			t.Fatal("invalid fallback block produced a count")
		}
	})
	t.Run("not-an-array", func(t *testing.T) { _, err := array([]byte(`{}`)); assertCap(t, err) })
	t.Run("primitive-id-types", func(t *testing.T) {
		if sameID([]byte(`"1"`), []byte(`1`)) {
			t.Fatal("strict string/number identity collapsed")
		}
	})
	t.Run("synthetic-response-metadata", func(t *testing.T) {
		original := mustOriginal(t, `{"type":"assistant","message":{"model":"<synthetic>","id":"must-not-anchor"}}`)
		r := record{Object: original[0]}
		if responseID(&r) != nil {
			t.Fatal("synthetic response id used in sibling walking")
		}
	})
}

func TestUnprovedPropertyErrorsDoNotTurnIntoZeroOrIdentity(t *testing.T) {
	for _, raw := range []string{`{"type":"assistant","message":null}`, `{"type":"user","message":123}`} {
		t.Run(raw, func(t *testing.T) { _, err := EstimateContext(mustOriginal(t, raw)); assertCap(t, err) })
	}
	for _, raw := range []string{`{"type":"assistant","message":{"model":"fixture-model","content":null,"usage":{"input_tokens":99,"output_tokens":1}}}`, `{"type":"assistant","message":{"model":"<synthetic>","usage":{"input_tokens":99,"output_tokens":1}}}`} {
		t.Run(raw, func(t *testing.T) {
			records := mustOriginal(t, raw)
			_, err := EstimateContext(records)
			assertCap(t, err)
			_, err = HasUsableContextUsage(records)
			assertCap(t, err)
		})
	}
	t.Run("signature-first-null", func(t *testing.T) {
		records := mustOriginal(t, `{"type":"assistant","message":{"model":"old","content":[null,{"type":"thinking","thinking":"secret"}]}}`)
		_, err := StripProtectedSignatures(records, ModelInput{Original: []byte(`"deepseek-flash"`)})
		assertCap(t, err)
	})
	t.Run("signature-later-null-after-some-short-circuit", func(t *testing.T) {
		records := mustOriginal(t, `{"type":"assistant","message":{"model":"old","content":[{"type":"thinking","thinking":"secret"},null]}}`)
		_, err := StripProtectedSignatures(records, ModelInput{Original: []byte(`"deepseek-flash"`)})
		assertCap(t, err)
	})
	t.Run("invalid-hidden-reasoning-json-parser", func(t *testing.T) {
		_, err := RoughContent([]byte(`[{"type":"redacted_thinking","data":"cc-haha:openai-reasoning:v1:{bad"}]`))
		assertCap(t, err)
	})
	t.Run("additional-unproved-cancel-display-sentinel", func(t *testing.T) {
		records := mustOriginal(t, `{"type":"assistant","message":{"model":"real","content":[{"type":"text","text":"The user doesn't want to take this action right now. STOP what you are doing and wait for the user to tell you how to proceed."}],"usage":{"input_tokens":99,"output_tokens":1}}}`)
		_, err := EstimateContext(records)
		assertCap(t, err)
	})
}

func TestUnprovedModelOwnTrimDoesNotUseNumericErrorText(t *testing.T) {
	original := mustOriginal(t, `{"type":"assistant","message":{"model":"old","content":[{"type":"thinking","thinking":"secret"}]}}`)
	for _, raw := range []string{`{"trim":false}`, `true`, `[]`} {
		t.Run(raw, func(t *testing.T) {
			_, err := StripProtectedSignatures(original, ModelInput{Original: []byte(raw)})
			assertCap(t, err)
		})
	}
}
