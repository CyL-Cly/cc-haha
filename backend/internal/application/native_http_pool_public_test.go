package application

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestActualPublicRunCommandHEADAndCanonicalReadRoundsReuseNativeTCP(t *testing.T) {
	for _, kind := range []string{"keepalive", "head-close", "head-failure"} {
		t.Run(kind, func(t *testing.T) {
			var mu sync.Mutex
			var ports []string
			posts := 0
			var file string
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				ports = append(ports, r.RemoteAddr)
				mu.Unlock()
				if r.Method == http.MethodHead {
					if r.Header.Get("Authorization") != "" || r.Header.Get("X-Api-Key") != "" {
						t.Error("HEAD inherited model credential")
					}
					if kind == "head-close" {
						w.Header().Set("Connection", "close")
					}
					if kind == "head-failure" {
						c, _, e := w.(http.Hijacker).Hijack()
						if e != nil {
							t.Error(e)
							return
						}
						_ = c.Close()
						return
					}
					w.WriteHeader(204)
					return
				}
				if r.Method != http.MethodPost {
					t.Error("unexpected method", r.Method)
					return
				}
				body, _ := io.ReadAll(r.Body)
				posts++
				if posts == 1 {
					if !strings.Contains(string(body), "ACTUAL_POOL_READ_HUMAN") {
						t.Error("ordinary human missing", string(body))
					}
					response(w, "pool-first", "Read", map[string]any{"file_path": file}, "")
					return
				}
				if posts != 2 || !strings.Contains(string(body), "ACTUAL_POOL_PHYSICAL_FILE") || !strings.Contains(string(body), "tool_result") {
					t.Error("canonical real Read was not in next Harness request", string(body))
				}
				response(w, "pool-second", "", nil, "ACTUAL_POOL_READ_RESPONSE")
			}))
			defer peer.Close()
			o := nativeSessionOptions(t, peer.URL)
			o.Stderr = io.Discard
			file = filepath.Join(o.WorkDir, "actual-pool-read.txt")
			if e := os.WriteFile(file, []byte("ACTUAL_POOL_PHYSICAL_FILE"), 0600); e != nil {
				t.Fatal(e)
			}
			command, _, e := ParseCommand([]string{"--bare", "--print", "--model", "fixture-model", "--tools", "Read", "--system-prompt", "private actual pool", "--thinking", "disabled", "ACTUAL_POOL_READ_HUMAN"}, o.Version, o.WorkDir, o.Native.Temp, o.Environment)
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			var out bytes.Buffer
			code, e := RunCommand(ctx, command, o, strings.NewReader(""), &out, nil)
			if e != nil || code != 0 || !strings.Contains(out.String(), "ACTUAL_POOL_READ_RESPONSE") {
				t.Fatal("actual public Read/model lifecycle", code, e, out.String())
			}
			mu.Lock()
			actual := append([]string{}, ports...)
			mu.Unlock()
			if len(actual) != 3 || posts != 2 || actual[1] != actual[2] {
				t.Fatal("actual two model requests did not reuse completed physical response", actual, posts)
			}
			if (actual[0] == actual[1]) != (kind == "keepalive") {
				t.Fatal("actual init HEAD eligibility differs from Source", kind, actual)
			}
		})
	}
}
