package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPublicCLIExplicitEmptyCustomPromptSkipsDefaultProducer(t *testing.T) {
	var request []byte
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		request, _ = io.ReadAll(r.Body)
		response(w, "explicit-empty-system", "", nil, "EMPTY_CUSTOM_SOURCE_OK")
	}))
	defer peer.Close()
	o := nativeSessionOptions(t, peer.URL)
	command, _, err := ParseCommand([]string{"--print", "--model", "fixture-model", "--system-prompt", "", "--tools", "", "--thinking", "disabled"}, o.Version, o.WorkDir, t.TempDir(), o.Environment)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var output bytes.Buffer
	code, err := RunCommand(ctx, command, o, strings.NewReader("ordinary private task"), &output, nil)
	if err != nil || code != 0 {
		t.Fatal(err, code, output.String())
	}
	if !strings.Contains(output.String(), "EMPTY_CUSTOM_SOURCE_OK") {
		t.Fatal("actual ordinary task not completed")
	}
	for _, defaultSection := range []string{"# Environment", "# Doing tasks", "# Using your tools", "You are an interactive agent"} {
		if bytes.Contains(request, []byte(defaultSection)) {
			t.Fatalf("explicit empty custom prompt invoked default section %q", defaultSection)
		}
	}
}
