package desktopoauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/providerproxy"
)

func TestOpenAICatalogCredentialHeadersMatchActualSourceByteStringGate(t *testing.T) {
	rows := []map[string]any{}
	for _, tokens := range []string{`{"accessToken":"fake","accountId":"owned"}`, `{"accessToken":"fake\ud800","accountId":"owned"}`, `{"accessToken":"界","accountId":"owned"}`, `{"accessToken":"fake\n"}`, `{"accessToken":"fake\u0000"}`, `{"accessToken":null,"accountId":false}`, `{"accessToken":42,"accountId":0}`, `{"accessToken":true,"accountId":{}}`, `{"accessToken":"fake","accountId":"界"}`, `{"accountId":"owned"}`} {
		rows = append(rows, map[string]any{"tokens": json.RawMessage(tokens), "models": []any{map[string]any{"slug": "remote", "visibility": "list"}}})
	}
	var source struct{ Observations []json.RawMessage }
	_ = json.Unmarshal(actualOpenAICatalogSource(t, map[string]any{"rows": rows}), &source)
	if len(source.Observations) != len(rows) {
		t.Fatal("Source header count")
	}
	for i, row := range rows {
		var expected struct {
			Value   []providerproxy.OpenAIModel
			Error   string
			Request struct{ Headers map[string]string }
		}
		_ = json.Unmarshal(source.Observations[i], &expected)
		var calls atomic.Int64
		var actualHeaders map[string]string
		host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			calls.Add(1)
			actualHeaders = map[string]string{}
			for _, name := range []string{"accept", "authorization", "chatgpt-account-id", "originator", "user-agent"} {
				if values := r.Header.Values(name); len(values) > 0 {
					actualHeaders[name] = values[0]
				}
			}
			_, _ = io.WriteString(w, `{"models":[{"slug":"remote","visibility":"list"}]}`)
		}))
		var now atomic.Int64
		now.Store(1770000000000)
		parent := catalogFixture(t, host.URL, &now)
		value, err := parent.OpenAI().fetchOpenAIModelCatalog(context.Background(), &Document{row["tokens"].(json.RawMessage)})
		if expected.Error != "" {
			if err == nil || calls.Load() != 0 {
				t.Errorf("row%d invalid ByteString reached native HTTP: %v", i, err)
			}
		} else {
			if err != nil {
				t.Errorf("row%d valid Source header rejected: %v", i, err)
			}
			if !bytes.Equal(encode(value), encode(expected.Value)) || !bytes.Equal(encode(actualHeaders), encode(expected.Request.Headers)) {
				t.Errorf("row%d original headers%s want%s", i, encode(actualHeaders), encode(expected.Request.Headers))
			}
		}
		_ = parent.Close()
		host.Close()
	}
}
