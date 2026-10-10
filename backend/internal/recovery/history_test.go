package recovery

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestInteractiveHistoryRetainsUndisplayableUTF16(t *testing.T) {
	var requests []request
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body request
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		requests = append(requests, body)
		io.WriteString(w, `{"content":[{"type":"text","text":"A\ud800B\udfffC"}]}`)
	}))
	defer server.Close()
	streams, stdout, stderr := fixtureIO(map[string]string{"ANTHROPIC_API_KEY": "fixture", "ANTHROPIC_MODEL": "fixture", "ANTHROPIC_BASE_URL": server.URL}, "first\nsecond\n/exit\n")
	code, err := Run(context.Background(), nil, streams)
	if code != 0 || err != nil || stderr.Len() != 0 || len(requests) != 2 {
		t.Fatalf("code=%d error=%v stderr=%q requests=%d", code, err, stderr, len(requests))
	}
	if raw := string(requests[1].Messages[1].Content); raw != `"A\ud800B\udfffC"` {
		t.Fatalf("display replaced model-visible history: %s", raw)
	}
	if !strings.Contains(stdout.String(), "claude> A�B�C\n") {
		t.Fatalf("terminal did not use UTF-8 replacement: %q", stdout)
	}
}
