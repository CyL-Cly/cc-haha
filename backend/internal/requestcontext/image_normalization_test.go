package requestcontext

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

const questionImage = `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+jR1MAAAAASUVORK5CYII="},"future":{"kept":"😀\ud800"}}`

func questionImageUser(uuid, content string) string {
	return `{"type":"user","uuid":"` + uuid + `","imagePasteIds":[4],"future":{"keep":true},"message":{"role":"user","content":` + content + `}}`
}

func questionImageAnchor(code, text, model string) string {
	return `{"type":"assistant","uuid":"error","isApiErrorMessage":true` + code + model + `,"message":{"id":"error-api","role":"assistant","model":"<synthetic>","content":[{"type":"text","text":` + text + `}]}}`
}

type questionImageCase struct {
	Name           string   `json:"name"`
	HistorySources []string `json:"historySources"`
	Model          string   `json:"model"`
	NonInteractive bool     `json:"nonInteractive"`
}

func questionImageCases() []questionImageCase {
	user := questionImageUser("image-user", `[{"type":"text","text":"before"},`+questionImage+`]`)
	only := questionImageUser("image-user", `[`+questionImage+`]`)
	next := questionImageUser("next-user", `[{"type":"text","text":"after"}]`)
	assistant := `{"type":"assistant","uuid":"ordinary","message":{"id":"ordinary","role":"assistant","content":[{"type":"text","text":"response"}]}}`
	code := `,"businessErrorCode":"image_unsupported"`
	anchor := questionImageAnchor(code, `"translated provider media rejection"`, `,"sourceModel":"fixture-model"`)
	legacy := `"This model does not support images. Continue with text, or switch to a vision-capable model and send the image again."`
	toolAssistant := `{"type":"assistant","uuid":"question-assistant","message":{"id":"question-api","role":"assistant","content":[{"type":"tool_use","id":"question-id","name":"AskUserQuestion","input":{"questions":[]}}]}}`
	toolUser := questionImageUser("question-result", `[{"type":"tool_result","tool_use_id":"question-id","content":"user answered"},{"type":"text","text":"approval feedback"},`+questionImage+`]`)
	deniedUser := questionImageUser("question-denied", `[{"type":"tool_result","tool_use_id":"question-id","is_error":true,"content":"user clarification"},`+questionImage+`]`)
	nestedUser := questionImageUser("question-result", `[{"type":"tool_result","tool_use_id":"question-id","content":[{"type":"text","text":"Image 1: 1×1 pixels; fixture screenshot"},`+questionImage+`]}]`)
	media := []string{
		`{"type":"image","source":{"type":"url","url":"https://private.invalid/image","future":"keep"},"cache_control":{"type":"ephemeral"},"future":{"keep":"\ud800"}}`,
		`{"type":"document","source":{"type":"base64","media_type":"application/pdf","data":"JVBERi0xLjQ="},"title":"Private title","context":"Private context","citations":{"enabled":true},"cache_control":{"type":"ephemeral"},"future":{"keep":"\ud800"}}`,
		`{"type":"document","source":{"type":"text","media_type":"text/plain","data":"Private text"}}`,
		`{"type":"document","source":{"type":"url","url":"https://private.invalid/doc"}}`,
		`{"type":"document","source":{"type":"content","content":"Private content"}}`,
		`{"type":"document","source":{"type":"content","content":[{"type":"text","text":"Private content"},{"type":"image","source":{"type":"url","url":"https://private.invalid/image"}}]}}`,
	}
	cases := []questionImageCase{
		{Name: "processed-image-preserved", HistorySources: []string{user}},
		{Name: "question-approval-image", HistorySources: []string{next, toolAssistant, toolUser}},
		{Name: "question-rejection-image-top-level", HistorySources: []string{next, toolAssistant, deniedUser}},
		{Name: "nested-mcp-screenshot-preserved", HistorySources: []string{next, toolAssistant, nestedUser}},
		{Name: "nested-mcp-screenshot-provider-recovery", HistorySources: []string{next, toolAssistant, nestedUser, anchor, next}},
		{Name: "nested-mcp-screenshot-model-switch", Model: "other-model", HistorySources: []string{next, toolAssistant, nestedUser, anchor, next}},
		{Name: "nested-mcp-screenshot-413", HistorySources: []string{next, toolAssistant, nestedUser, questionImageAnchor(`,"businessErrorCode":"request_too_large"`, `"translated request error"`, ""), next}},
		{Name: "adjacent-user-images", HistorySources: []string{user, next}},
		{Name: "translated-code-recovery", HistorySources: []string{user, anchor, next}},
		{Name: "model-switch-replays-image", Model: "other-model", HistorySources: []string{user, anchor, next}},
		{Name: "legacy-image-recovery", HistorySources: []string{user, questionImageAnchor("", legacy, ""), next}},
		{Name: "legacy-interactive-image-recovery", HistorySources: []string{user, questionImageAnchor("", `"Image was too large. Double press esc to go back and try again with a smaller image."`, ""), next}},
		{Name: "image-only-error-turn-removed", HistorySources: []string{only, anchor, next}},
		{Name: "ordinary-assistant-stops-anchor-search", HistorySources: []string{user, assistant, anchor, next}},
		{Name: "virtual-anchor-does-not-strip", HistorySources: []string{user, strings.Replace(anchor, `"isApiErrorMessage":true`, `"isApiErrorMessage":true,"isVirtual":true`, 1), next}},
		{Name: "whole-request-413-placeholders", HistorySources: []string{user, assistant, only, questionImageAnchor(`,"businessErrorCode":"request_too_large"`, `"translated request error"`, ""), questionImageUser("fresh-image", `[`+questionImage+`]`)}},
		{Name: "legacy-413-placeholders", HistorySources: []string{user, questionImageAnchor("", `"Request too large (max 20MB). Try with a smaller file."`, ""), next}},
		{Name: "explicit-other-code-suppresses-legacy-413", HistorySources: []string{user, questionImageAnchor(`,"businessErrorCode":"other_code"`, `"Request too large (max 20MB). Try with a smaller file."`, ""), next}},
		{Name: "unknown-code-falls-back-to-legacy-image", HistorySources: []string{user, questionImageAnchor(`,"businessErrorCode":"other_code"`, legacy, ""), next}},
		{Name: "multiple-errors-find-same-user", HistorySources: []string{user, questionImageAnchor("", `"ordinary error"`, ""), anchor, next}},
	}
	for i, block := range media {
		cases = append(cases, questionImageCase{Name: fmt.Sprintf("sdk-media-source-%d", i), HistorySources: []string{questionImageUser("media-user", `[{"type":"text","text":"before"},`+block+`]`)}})
		if i > 0 {
			cases = append(cases, questionImageCase{Name: fmt.Sprintf("sdk-document-recovery-%d", i), HistorySources: []string{questionImageUser("doc-user", `[`+block+`]`), questionImageAnchor(`,"businessErrorCode":"pdf_invalid"`, `"Translated document failure"`, `,"sourceModel":"fixture-model"`), next}})
			cases = append(cases, questionImageCase{Name: fmt.Sprintf("nested-document-source-%d", i), HistorySources: []string{next, toolAssistant, questionImageUser("doc-result", `[{"type":"tool_result","tool_use_id":"question-id","content":[`+block+`]}]`)}})
		}
	}
	for i := range cases {
		if cases[i].Model == "" {
			cases[i].Model = "fixture-model"
		}
		cases[i].NonInteractive = cases[i].Name != "legacy-interactive-image-recovery"
	}
	return cases
}

func runQuestionImageSource(t *testing.T, fixture string, input any, extraEnv []string) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun runner not configured")
	}
	home := t.TempDir()
	config := filepath.Join(home, ".claude")
	_ = os.Mkdir(config, 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	payload, _ := json.Marshal(input)
	var values map[string]json.RawMessage
	_ = json.Unmarshal(payload, &values)
	values["homeDir"], _ = json.Marshal(home)
	payload, _ = json.Marshal(values)
	path, _ := filepath.Abs(filepath.Join("../../../scripts/quality-gate/go-parity", fixture))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", path)
	cmd.Dir = home
	cmd.Env = append([]string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}, extraEnv...)
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatalf("actual Source %s %v %.2400s", fixture, err, stderr.String())
	}
	return output
}

func TestQuestionImageNormalizationMatchesActualSource(t *testing.T) {
	cases := questionImageCases()
	sizes := []struct {
		Lengths []int  `json:"lengths"`
		Units   string `json:"units"`
	}{{Lengths: []int{APIImageMaxBase64Size}}, {Lengths: []int{APIImageMaxBase64Size + 1}}, {Lengths: []int{APIImageMaxBase64Size + 1, 2, APIImageMaxBase64Size + 1024}}, {Lengths: []int{APIImageMaxBase64Size + 2}, Units: "astral"}}
	output := runQuestionImageSource(t, "native-question-image.fixture.ts", map[string]any{"cases": cases, "sizes": sizes}, []string{"CC_HAHA_NATIVE_QUESTION_IMAGE_FIXTURE=1"})
	var source struct {
		Normalized []struct {
			Messages  []string
			Unchanged bool
			Error     any
		}
		Sizes []struct {
			OK    bool `json:"ok"`
			Error *struct{ Name, Message string }
		}
		Capabilities struct{ Blocked []string }
	}
	if json.Unmarshal(output, &source) != nil || len(source.Normalized) != len(cases) || len(source.Sizes) != len(sizes) {
		t.Fatalf("actual image Source shape %.1200s", output)
	}
	if len(source.Capabilities.Blocked) > 0 {
		t.Fatal("Source escaped fixture", source.Capabilities.Blocked)
	}
	for i, item := range cases {
		t.Run(item.Name, func(t *testing.T) {
			input, err := fixtureContext(fixtureInput{HistorySources: item.HistorySources}, "http://127.0.0.1:43210")
			if err != nil {
				t.Fatal(err)
			}
			before, _ := json.Marshal(input.History)
			got, err := normalizeHistoryForModel(input.History, dependencies(), nil, item.Model, item.NonInteractive)
			if err != nil || source.Normalized[i].Error != nil {
				t.Fatalf("Go %v Source %v", err, source.Normalized[i].Error)
			}
			want := source.Normalized[i].Messages
			actual := []string{}
			for _, message := range got {
				raw, _ := stringify(rawObject(message))
				actual = append(actual, string(raw))
			}
			if !reflect.DeepEqual(actual, want) {
				t.Fatalf("actual image normalization differs\nGo %s\nSource %s", actual, want)
			}
			after, _ := json.Marshal(input.History)
			if !bytes.Equal(before, after) || !source.Normalized[i].Unchanged {
				t.Fatal("normalization mutated original history")
			}
		})
	}
	for i, item := range sizes {
		parts := []protocol.JSONObject{}
		for _, length := range item.Lengths {
			data := strings.Repeat("A", length)
			if item.Units == "astral" {
				data = strings.Repeat("😀", length/2)
			}
			imageSource := object(field("type", quoted("base64")), field("media_type", quoted("image/png")), field("data", quoted(data)))
			parts = append(parts, object(field("type", quoted("image")), field("source", rawObject(imageSource))))
		}
		raw, _ := json.Marshal(parts)
		var message protocol.JSONObject
		_ = message.UnmarshalJSON([]byte(questionImageUser("size", string(raw))))
		err := ValidateUserImagesForAPI([]protocol.JSONObject{message})
		if source.Sizes[i].OK {
			if err != nil {
				t.Fatal(err)
			}
		} else {
			var limit *ImageSizeError
			if !errors.As(err, &limit) || source.Sizes[i].Error == nil || source.Sizes[i].Error.Name != "ImageSizeError" || err.Error() != source.Sizes[i].Error.Message {
				t.Fatalf("size case %d Go %v Source %+v", i, err, source.Sizes[i])
			}
		}
	}
}

func TestQuestionImagePhysicalRequestsMatchActualSource(t *testing.T) {
	for _, index := range []int{0, 1, 2, 4, 11, 20, 21, 23, 24, 27, 30, 33} {
		item := questionImageCases()[index]
		t.Run(item.Name, func(t *testing.T) {
			scenario := fixtureInput{Name: item.Name, HistorySources: item.HistorySources}
			output := runQuestionImageSource(t, "request-context.fixture.ts", scenario, []string{"CC_HAHA_REQUEST_CONTEXT_FIXTURE=1", "CLAUDE_CODE_SIMPLE=1", "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK=1", "ANTHROPIC_API_KEY=offline-fixture-key"})
			var source struct {
				Observation fixtureObservation
				SourceOnly  struct {
					WireRequests []fetchObservation
					Runtime      struct{ Platform, Arch, Version string }
					Capabilities struct{ Blocked []string }
				}
			}
			if json.Unmarshal(output, &source) != nil || len(source.SourceOnly.WireRequests) != 1 || len(source.Observation.Fetches) != 1 {
				t.Fatalf("physical Source shape %.1200s", output)
			}
			if source.SourceOnly.WireRequests[0].Body != source.Observation.Fetches[0].Body || len(source.SourceOnly.Capabilities.Blocked) > 0 {
				t.Fatal("Source actual wire not captured or fixture escaped")
			}
			scenario.Runtime.Platform, scenario.Runtime.Arch, scenario.Runtime.Version = source.SourceOnly.Runtime.Platform, source.SourceOnly.Runtime.Arch, source.SourceOnly.Runtime.Version
			got, err := observeFixture(scenario)
			if err != nil {
				t.Fatal(err)
			}
			if len(got.wireRequests) != 1 || got.wireRequests[0].Body != got.Fetches[0].Body {
				t.Fatal("Go actual loopback POST not captured")
			}
			got.wireRequests = nil
			if !reflect.DeepEqual(got, source.Observation) {
				wantJSON, _ := json.Marshal(source.Observation)
				gotJSON, _ := json.Marshal(got)
				t.Fatalf("physical request and prepare differ\nGo %s\nSource %s", gotJSON, wantJSON)
			}
		})
	}
}
