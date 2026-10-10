package apipresentation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/imageprocessing"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestcontext"
	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy"
)

type mediaSourceCase struct {
	Kind, Message, Model string
	Status               *int
	Payload              json.RawMessage
	History              []protocol.JSONObject
	NonInteractive       bool
	Match                bool
	Wrapped              bool
}

func mediaSourceRows() []mediaSourceCase {
	rows := []mediaSourceCase{}
	basic := []struct {
		Text  string
		Match bool
	}{
		{"This model does not support image blocks", true}, {"unsupported modality: image input is not available", true}, {"image_url content parts are not allowed for this model", true}, {"image_url expected text", true}, {"maximum of 100 PDF pages", true}, {"The PDF specified is password protected", true}, {"The PDF specified was not valid", true}, {"image exceeds 5 MB maximum", true}, {"image dimensions exceed many-image limit", true}, {"UNRECOGNIZED PROVIDER WORDING", false}, {"prompt is too long image exceeds maximum", false}, {"Opus is experiencing high load, please use /model to switch to Sonnet image exceeds maximum", false}, {"The PDF specified was not valid image unsupported", true},
	}
	for _, nonInteractive := range []bool{false, true} {
		for _, item := range basic {
			for _, kind := range []string{"error", "api"} {
				status := 400
				rows = append(rows, mediaSourceCase{Kind: kind, Message: item.Text, Model: "fixture-model", Status: &status, NonInteractive: nonInteractive, Match: item.Match && (kind == "api" || item.Text != "image exceeds 5 MB maximum" && item.Text != "image dimensions exceed many-image limit")})
			}
		}
		for _, kind := range []string{"resize", "size"} {
			rows = append(rows, mediaSourceCase{Kind: kind, Message: "local image failure", Model: "fixture-model", NonInteractive: nonInteractive, Match: true})
		}
		for _, status := range []int{400, 422, 401, 403, 404, 500} {
			for _, kind := range []string{"image", "nested", "future", "none"} {
				block := `{"type":"image","source":{"type":"base64","media_type":"image/png","data":"abc😀\\ud800"}}`
				content := `[` + block + `]`
				if kind == "nested" {
					content = `[{"type":"tool_result","tool_use_id":"actual","content":[` + block + `]}]`
				} else if kind == "none" {
					content = `[{"type":"text","text":"😀\\ud800"}]`
				} else if kind == "future" {
					content = `[{"type":"text","text":"private","future":` + block + `}]`
				}
				var user protocol.JSONObject
				_ = user.UnmarshalJSON([]byte(`{"type":"user","message":{"role":"user","content":` + content + `}}`))
				copyStatus := status
				rows = append(rows, mediaSourceCase{Kind: "api", Message: "Only text is allowed by this provider", Model: "fixture-model", Status: &copyStatus, NonInteractive: nonInteractive, History: []protocol.JSONObject{user}, Match: kind != "none" && (status == 400 || status == 422), Wrapped: true})
			}
		}
		for _, content := range []string{`"plain 😀 \ud800"`, `[{"type":"document","source":{"type":"base64","data":"AAAA😀\ud800"}},{"type":"text","text":"你好👋"}]`, `[{"type":"tool_result","tool_use_id":"x","content":[{"type":"image","source":{"type":"url","url":"https://private.invalid/😀"}},{"type":"future","value":"你好\ud800"}]}]`, `[]`} {
			var user protocol.JSONObject
			_ = user.UnmarshalJSON([]byte(`{"type":"user","message":{"role":"user","content":` + content + `}}`))
			status := 413
			rows = append(rows, mediaSourceCase{Kind: "api", Message: "large relay " + string(bytes.Repeat([]byte("😀"), 300)), Model: "fixture-model", Status: &status, NonInteractive: nonInteractive, History: []protocol.JSONObject{user}, Match: true})
		}
		status := 413
		rows = append(rows, mediaSourceCase{Kind: "api", Message: "relay without prepared history", Model: "fixture-model", Status: &status, NonInteractive: nonInteractive, Match: true})
	}
	return rows
}

func TestNativeMediaErrorPresentationMatchesActualSource(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	rows := mediaSourceRows()
	home := t.TempDir()
	_ = os.Mkdir(filepath.Join(home, ".claude"), 0700)
	_ = os.Mkdir(filepath.Join(home, "tmp"), 0700)
	sourceRows := []map[string]any{}
	for _, row := range rows {
		values := map[string]any{"kind": row.Kind, "message": row.Message, "model": row.Model, "nonInteractive": row.NonInteractive}
		if row.Status != nil && row.Kind == "api" {
			values["status"] = *row.Status
		}
		if row.Payload != nil {
			values["payload"] = row.Payload
		}
		if row.History != nil {
			values["history"] = row.History
		}
		sourceRows = append(sourceRows, values)
	}
	payload, _ := json.Marshal(map[string]any{"homeDir": home, "rows": sourceRows})
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-media-error.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + filepath.Join(home, "tmp"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_NATIVE_MEDIA_ERROR_FIXTURE=1", "NODE_ENV=production", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "DISABLE_TELEMETRY=1", "ANTHROPIC_API_KEY=offline-fake-media-key", "ANTHROPIC_BASE_URL=https://127.0.0.1:1", "CLAUDE_CODE_DISABLE_FAST_MODE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	output, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source struct {
		Rows []struct {
			Message, Value string
			Allocations    []string
		}
	}
	if err := json.Unmarshal(output, &source); err != nil || len(source.Rows) != len(rows) {
		t.Fatal(err, string(output))
	}
	domain, _ := DomainFromOriginalEnv(originalDomainEnv())
	for i, row := range rows {
		calls := 0
		allocations := []string{}
		factory, _ := messages.NewFactory(func() string {
			calls++
			allocations = append(allocations, fmt.Sprintf("uuid:%d", calls))
			return fmt.Sprintf("media-id-%d", calls)
		}, func() time.Time { allocations = append(allocations, "date"); return time.UnixMilli(1700000000000) })
		var failure error
		switch row.Kind {
		case "api":
			class := "APIError"
			if row.Status != nil {
				switch *row.Status {
				case 400:
					class = "BadRequestError"
				case 401:
					class = "AuthenticationError"
				case 403:
					class = "PermissionDeniedError"
				case 404:
					class = "NotFoundError"
				case 422:
					class = "UnprocessableEntityError"
				case 500:
					class = "InternalServerError"
				}
			}
			failure = &provider.SDKStreamError{Kind: "http_api", Class: class, Message: source.Rows[i].Message, MessageJSON: quote(source.Rows[i].Message), Status: row.Status, Payload: row.Payload}
		case "resize":
			failure = &imageprocessing.ResizeError{Message: row.Message}
		case "size":
			failure = &requestcontext.ImageSizeError{Images: []requestcontext.OversizedImage{{Index: 1, Size: 5242881}}}
		default:
			failure, _ = NewOriginalError(quote(source.Rows[i].Message))
		}
		if row.Wrapped {
			failure = &retrypolicy.CannotRetryError{Original: failure}
		}
		value, matched, err := NativeMediaFailure(failure, domain, factory, MediaOptions{Model: row.Model, NonInteractive: row.NonInteractive, MessagesForAPI: row.History})
		if err != nil || matched != row.Match {
			t.Errorf("case%d %s/%s status%v matched%v want%v error%v", i, row.Kind, row.Message, row.Status, matched, row.Match, err)
			continue
		}
		if !matched {
			if value != nil || calls != 0 {
				t.Errorf("nonmedia%d allocated", i)
			}
			continue
		}
		got, _ := protocol.ParseAndStringifyJavaScriptJSON(rawMedia(value.Record))
		want, _ := protocol.ParseAndStringifyJavaScriptJSON([]byte(source.Rows[i].Value))
		if string(got) != string(want) || !reflect.DeepEqual(allocations, source.Rows[i].Allocations) {
			t.Errorf("case%d actual Source mismatch\nGo %s allocations%v\nSource %s allocations%v", i, got, allocations, want, source.Rows[i].Allocations)
		}
	}
}
func rawMedia(value protocol.JSONObject) json.RawMessage { raw, _ := value.MarshalJSON(); return raw }
