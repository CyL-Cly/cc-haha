package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestPromptModeConsumesOneExpandedPasteWithoutDisplayMetadata(t *testing.T) {
	var calls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		body, _ := io.ReadAll(r.Body)
		if bytes.Count(body, []byte("expanded source text")) != 1 || bytes.Contains(body, []byte("[Pasted text #1]")) {
			t.Errorf("UI display metadata changed model prompt: %s", body)
		}
		response(w, "paste-done", "", nil, "PASTE DONE")
	}))
	defer provider.Close()
	s, err := NewSession(context.Background(), nativeSessionOptions(t, provider.URL))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(context.Background(), tui.Input{Mode: "prompt", Text: "expanded source text", PreExpansionText: "[Pasted text #1]", PastedContents: map[int]tui.PastedContent{1: {ID: 1, Type: "text", Content: "expanded source text"}}}, nil); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("prompt mode did not execute once: %d", calls.Load())
	}
}
