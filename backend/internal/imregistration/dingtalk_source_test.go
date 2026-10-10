package imregistration

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestRegistrationMatchesActualOriginalHandlerAndHTTPPersistence(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Fatal("actual Source Bun required")
	}
	seed := map[string]any{"future": map[string]any{"keep": true}, "telegram": map[string]any{"botToken": "fake-other-retained"}, "dingtalk": map[string]any{"allowedUsers": []string{"fake-user"}, "future": 7}}
	type reply struct {
		Status int    `json:"status,omitempty"`
		Body   any    `json:"body,omitempty"`
		Raw    string `json:"raw,omitempty"`
	}
	type row struct {
		Seed    any     `json:"seed"`
		Action  string  `json:"action"`
		Input   any     `json:"input"`
		Replies []reply `json:"replies"`
	}
	var rows []row
	begin := map[string]any{"errcode": 0, "device_code": " private-device ", "verification_uri_complete": " https://private.example.test/confirm?code=TEST_296 ", "user_code": " USER ", "verification_uri": " https://private.example.test/confirm "}
	clone := func(input map[string]any) map[string]any {
		out := map[string]any{}
		for k, v := range input {
			out[k] = v
		}
		return out
	}
	for _, d := range []any{nil, 3, "0x10", true, false, "", []any{17}, []any{}, map[string]any{}, -1, "Infinity", 1.5} {
		b := clone(begin)
		b["expires_in"] = d
		b["interval"] = d
		rows = append(rows, row{seed, "begin", map[string]any{}, []reply{{Body: map[string]any{"errcode": 0, "nonce": "  nonce  "}}, {Body: b}}})
	}
	for _, first := range []reply{{Body: map[string]any{"errcode": 0}}, {Body: map[string]any{"errcode": "0", "nonce": "n"}}, {Body: map[string]any{"errcode": false, "nonce": "n"}}, {Status: 500, Body: map[string]any{"errcode": 73, "errmsg": "private rejection"}}, {Raw: "not JSON"}, {Raw: "null"}} {
		rows = append(rows, row{seed, "begin", map[string]any{}, []reply{first}})
	}
	for _, missing := range []string{"device_code", "verification_uri_complete"} {
		b := clone(begin)
		delete(b, missing)
		rows = append(rows, row{seed, "begin", nil, []reply{{Body: map[string]any{"errcode": 0, "nonce": "n"}}, {Body: b}}})
	}
	for _, status := range []any{"SUCCESS", " success ", "PENDING", "FAIL", "ß", " ﬃ ", "", nil, 17, []any{"wait"}} {
		rows = append(rows, row{seed, "poll", map[string]any{"deviceCode": " device "}, []reply{{Body: map[string]any{"errcode": 0, "status": status, "client_id": " fake-client ", "client_secret": " fake-secret ", "fail_reason": " reason "}}}})
	}
	rows = append(rows, row{seed, "poll", map[string]any{"deviceCode": "device"}, []reply{{Body: map[string]any{"errcode": 0, "status": "SUCCESS", "client_id": "fake-client"}}}})
	for _, device := range []any{nil, "", 17, strings.Repeat("x", 257)} {
		rows = append(rows, row{seed, "poll", map[string]any{"deviceCode": device}, nil})
	}
	private := t.TempDir()
	config := filepath.Join(private, ".claude")
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	input, _ := json.Marshal(map[string]any{"rows": rows})
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixturePath(t))
	cmd.Dir = private
	cmd.Env = []string{"HOME=" + private, "USERPROFILE=" + private, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + private, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(private), "CC_HAHA_DINGTALK_REGISTRATION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal("actual Source", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(out, &expected); err != nil {
		t.Fatal(err, string(out))
	}
	if len(expected) != len(rows) {
		t.Fatal("Source row count")
	}
	owned := t.TempDir()
	store := storage.New(storage.Options{})
	adapters, err := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: owned, Store: store, LogError: func(error) {}})
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range rows {
		var requests []any
		at := 0
		peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			var body any
			raw, e := io.ReadAll(req.Body)
			if e != nil || json.Unmarshal(raw, &body) != nil {
				t.Errorf("actual request decode %v", e)
			}
			requests = append(requests, map[string]any{"path": req.URL.Path, "method": req.Method, "body": body})
			if at >= len(r.Replies) {
				t.Error("unexpected physical request")
				w.WriteHeader(500)
				return
			}
			res := r.Replies[at]
			at++
			w.Header().Set("Content-Type", "application/json")
			status := res.Status
			if status == 0 {
				status = 200
			}
			w.WriteHeader(status)
			if res.Raw != "" {
				io.WriteString(w, res.Raw)
			} else {
				json.NewEncoder(w).Encode(res.Body)
			}
		}))
		file := filepath.Join(owned, "adapters.json")
		raw, _ := json.Marshal(seed)
		if e := os.WriteFile(file, raw, 0600); e != nil {
			t.Fatal(e)
		}
		service, e := New(Options{Client: peer.Client(), BaseURL: peer.URL, Source: " PRIVATE_NEXT_SOURCE ", Adapters: adapters})
		if e != nil {
			t.Fatal(e)
		}
		status := 200
		var bodyRaw []byte
		if r.Action == "begin" {
			result, e := service.Begin(ctx)
			err = e
			if e == nil {
				bodyRaw, _ = result.MarshalJSON()
			}
		} else {
			value, ok := r.Input.(map[string]any)["deviceCode"].(string)
			if !ok || protocolTrim(value) == "" || len(value) > 256 {
				err = &Error{400, "BAD_REQUEST", "deviceCode is required"}
			} else {
				result, e := service.Poll(ctx, protocolTrim(value))
				err = e
				if e == nil {
					bodyRaw, _ = result.MarshalJSON()
				}
			}
		}
		peer.Close()
		var body map[string]any
		if err != nil {
			var api *Error
			var escaped *PollFailure
			if errors.As(err, &escaped) {
				status = 500
				body = map[string]any{"error": "Internal server error"}
			} else if errors.As(err, &api) {
				status = api.Status
				body = map[string]any{"error": api.Code, "message": api.Message}
			} else {
				status = 500
				body = map[string]any{"error": "INTERNAL_ERROR", "message": "An unexpected error occurred"}
			}
		} else if e = json.Unmarshal(bodyRaw, &body); e != nil {
			t.Fatal(e)
		}
		if qr, ok := body["qrDataUrl"].(string); ok {
			pngBytes, e := base64.StdEncoding.DecodeString(strings.TrimPrefix(qr, "data:image/png;base64,"))
			if e != nil {
				t.Fatal(e)
			}
			image, e := png.Decode(bytes.NewReader(pngBytes))
			if e != nil || image.Bounds().Dx() != 220 || image.Bounds().Dy() != 220 {
				t.Fatal("actual QR PNG", e)
			}
			delete(body, "qrDataUrl")
			if expected[i]["qr"] == nil {
				t.Errorf("row%d original QR missing", i)
			}
		} else if expected[i]["qr"] != nil {
			t.Errorf("row%d Go QR missing", i)
		}
		persisted, e := os.ReadFile(file)
		if e != nil {
			t.Fatal(e)
		}
		var stored any
		_ = json.Unmarshal(persisted, &stored)
		if requests == nil {
			requests = []any{}
		}
		got := map[string]any{"status": float64(status), "body": body, "requests": requests, "raw": stored}
		delete(expected[i], "qr")
		if !reflect.DeepEqual(got, expected[i]) {
			t.Errorf("row%d actual registration/physical requests/persistence\nGo %v\nSource %v", i, got, expected[i])
		}
	}
}

func fixturePath(t *testing.T) string {
	t.Helper()
	dir, e := filepath.Abs("../../../scripts/quality-gate/go-parity/native-dingtalk-registration.fixture.ts")
	if e != nil {
		t.Fatal(e)
	}
	return dir
}
func protocolTrim(s string) string { return strings.TrimSpace(s) }
