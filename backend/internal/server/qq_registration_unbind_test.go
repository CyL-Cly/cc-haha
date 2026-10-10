package server

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/adapterconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

func TestQQConsumerUnbindUsesActualWriterAndRetainsUnknownFields(t *testing.T) {
	private := t.TempDir()
	file := filepath.Join(private, "adapters.json")
	seed := `{"future":17,"qq":{"appId":"old","appSecret":"old-secret","allowedUsers":["old"],"pairedUsers":[{"id":"old"}],"future":true,"workspace":"keep"}}`
	if e := os.WriteFile(file, []byte(seed), 0600); e != nil {
		t.Fatal(e)
	}
	adapters, e := adapterconfig.NewService(adapterconfig.ServiceOptions{ConfigDir: private, Store: storage.New(storage.Options{}), LogError: func(error) {}})
	if e != nil {
		t.Fatal(e)
	}
	s := &Server{ctx: context.Background()}
	response := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/api/adapters/qq/unbind", strings.NewReader("ignored malformedJSON"))
	if e := s.qqRegistrationHTTP(response, req, []string{"unbind"}, nil, adapters, func(e error) error { return e }); e != nil {
		t.Fatal(e)
	}
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	var value map[string]any
	if e := json.Unmarshal(response.Body.Bytes(), &value); e != nil {
		t.Fatal(e)
	}
	section := value["qq"].(map[string]any)
	if section["appId"] != nil || section["appSecret"] != nil || section["future"] != true || section["workspace"] != "keep" || len(section["allowedUsers"].([]any)) != 0 || len(section["pairedUsers"].([]any)) != 0 || value["future"] != float64(17) {
		t.Fatal("original unbind response/storage", value)
	}
	for _, method := range []string{"GET", "PUT"} {
		response := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/api/adapters/qq/unbind", nil)
		e := s.qqRegistrationHTTP(response, req, []string{"unbind"}, nil, adapters, func(e error) error { return e })
		api, ok := e.(*apiError)
		if !ok || api.status != 404 {
			t.Fatal("original unknown-method boundary", e)
		}
	}
}

func TestQQConsumerRedrawsOnlyActualRotatedVerificationURL(t *testing.T) {
	for _, raw := range []string{"", `null`, `""`, `17`, `"https://q.qq.com/qqbot/openclaw/connect.html?task_id=rotated"`} {
		value := protocol.NewJSONObject(protocol.JSONField{Name: "connected", Value: json.RawMessage(`false`)}, protocol.JSONField{Name: "status", Value: json.RawMessage(`"waiting"`)})
		if raw != "" {
			value.Set("verificationUrl", json.RawMessage(raw))
		}
		appendQQQR(&value, value.Get("verificationUrl"))
		if strings.Contains(raw, "rotated") {
			if !strings.HasPrefix(value.String("qrDataUrl"), "data:image/png;base64,") {
				t.Fatal("rotated QR not redrawn")
			}
		} else if value.Get("qrDataUrl") != nil {
			t.Fatal("poll produced unsolicited QR", raw)
		}
	}
}
