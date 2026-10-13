package clibootstrap

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
)

func TestActualSourceMCPTextAndNotebookLargeReadCounter(t *testing.T) {
	if os.Getenv("CC_HAHA_SOURCE_BUN") == "" {
		t.Skip("actual Source Bun required")
	}
	for _, status := range []int{200, 404} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			home, env := ordinaryFixture(t)
			type observed struct {
				Path, Beta string
				Body       json.RawMessage
			}
			var mu sync.Mutex
			requests := []observed{}
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				mu.Lock()
				requests = append(requests, observed{r.URL.RequestURI(), r.Header.Get("anthropic-beta"), append(json.RawMessage(nil), body...)})
				mu.Unlock()
				if r.URL.Path != "/v1/messages/count_tokens" {
					t.Error("MCP Read triggered message sampling", r.URL.Path)
				}
				w.Header().Set("content-type", "application/json")
				w.WriteHeader(status)
				if status == 200 {
					io.WriteString(w, `{"input_tokens":23}`)
				} else {
					io.WriteString(w, `{"type":"error","error":{"type":"not_found_error","message":"unsupported count"}}`)
				}
			}))
			defer peer.Close()
			env["ANTHROPIC_BASE_URL"], env["ANTHROPIC_MODEL"] = peer.URL, "fixture-model"
			textPath, notebookPath := filepath.Join(home, "actual-long-history.txt"), filepath.Join(home, "actual-large-notebook.ipynb")
			if err := os.WriteFile(textPath, []byte(strings.Repeat("physical history", 2200)), 0600); err != nil {
				t.Fatal(err)
			}
			notebook := map[string]any{"cells": []map[string]any{{"cell_type": "code", "id": "actual-long-cell", "source": []string{strings.Repeat("print('actual')", 2200)}, "metadata": map[string]any{}, "outputs": []any{}, "execution_count": nil}}, "metadata": map[string]any{"kernelspec": map[string]any{"language": "python"}}, "nbformat": 4, "nbformat_minor": 5}
			if err := os.WriteFile(notebookPath, rawMain(notebook), 0600); err != nil {
				t.Fatal(err)
			}
			paths := []string{textPath, notebookPath}
			source := startOrdinarySource(t, home, home, env)
			want := []any{}
			for _, path := range paths {
				want = append(want, mcpOrdinaryResult(t, source.call(t, "Read", map[string]any{"file_path": path})))
			}
			source.close(t)
			mu.Lock()
			sourceRequests := append([]observed{}, requests...)
			requests = nil
			mu.Unlock()
			host, _ := ordinaryGoContext(t, home, home, env)
			got := []any{}
			for _, path := range paths {
				data, err := host.Call(context.Background(), "Read", rawMain(map[string]any{"file_path": path}))
				if err != nil {
					t.Fatal(err)
				}
				got = append(got, mcpOrdinaryResult(t, data))
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatal("actual MCP Source Read result differs")
			}
			mu.Lock()
			goRequests := append([]observed{}, requests...)
			mu.Unlock()
			if !reflect.DeepEqual(sourceRequests, goRequests) {
				t.Fatal("actual MCP Source raw count request differs", len(sourceRequests), len(goRequests))
			}
			if len(goRequests) != 2 {
				t.Fatal("Read/Notebook count not wired or extra sampling", len(goRequests))
			}
		})
	}
}
