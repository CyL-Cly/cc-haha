package clibootstrap

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type coverage893Reader struct{ reason error }

func (r coverage893Reader) Read([]byte) (int, error) { return 0, r.reason }

type coverage893Writer struct{ reason error }

func (w coverage893Writer) Write([]byte) (int, error) { return 0, w.reason }
func TestCoverage893PublicArgumentFailuresBeforeNativeStartup(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func([]string) []string
	}{{"zero-turns", func(a []string) []string {
		for i := range a {
			if a[i] == "--max-turns" {
				a[i+1] = "0"
			}
		}
		return a
	}}, {"fractional-turns", func(a []string) []string {
		for i := range a {
			if a[i] == "--max-turns" {
				a[i+1] = "1.5"
			}
		}
		return a
	}}, {"missing-session", func(a []string) []string { return a[:len(a)-2] }}, {"unsupported-tool", func(a []string) []string {
		for i := range a {
			if a[i] == "--tools" {
				a[i+1] = "Read"
			}
		}
		return a
	}}, {"positional", func(a []string) []string { return append(a, "hello") }}} {
		t.Run(tc.name, func(t *testing.T) {
			if _, e := ParsePrintArguments(tc.change(mainRecipeArgs()), "fixture"); e == nil {
				t.Fatal("invalid public arguments admitted")
			}
		})
	}
}
func TestCoverage893PublicInputAndOutputFailuresUseSameMainGraph(t *testing.T) {
	for _, name := range []string{"native-read-error", "malformed-json", "unknown-local-command", "output-error", "unterminated-local-input"} {
		t.Run(name, func(t *testing.T) {
			n := nativeForTest(t)
			var posts atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				posts.Add(1)
				http.Error(w, "undeclared model request", 422)
			}))
			defer server.Close()
			n.Environment["ANTHROPIC_BASE_URL"] = server.URL
			args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
			if e != nil {
				t.Fatal(e)
			}
			reason := errors.New("original external I/O failure")
			var out, errOut bytes.Buffer
			var input io.Reader = strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"/cost\"}}\n")
			var output io.Writer = &out
			switch name {
			case "native-read-error":
				input = coverage893Reader{reason}
			case "malformed-json":
				input = strings.NewReader("{\n")
			case "unknown-local-command":
				input = strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"/clear\"}}\n")
			case "output-error":
				output = coverage893Writer{reason}
			case "unterminated-local-input":
				input = strings.NewReader("{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"/cost\"}}")
			}
			p, e := NewMainPrint(MainPrintOptions{Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: output, Error: &errOut})
			if e != nil {
				t.Fatal(e)
			}
			defer p.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			code, e := p.Run(ctx)
			if name == "unterminated-local-input" {
				if e != nil || code != 0 {
					t.Fatal(code, e)
				}
				var rows []map[string]any
				for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
					var row map[string]any
					if e := json.Unmarshal([]byte(line), &row); e != nil {
						t.Fatal(e, line)
					}
					rows = append(rows, row)
				}
				if len(rows) != 3 || rows[0]["type"] != "system" || rows[0]["subtype"] != "init" || rows[1]["type"] != "assistant" || rows[2]["type"] != "result" || rows[2]["subtype"] != "success" || rows[2]["total_cost_usd"] != float64(0) {
					t.Fatal("actual EOF tail did not execute local cost in FIFO", rows)
				}
			} else {
				if e == nil || code != 1 {
					t.Fatal(code, e)
				}
				if (name == "native-read-error" || name == "output-error") && !errors.Is(e, reason) {
					t.Fatal("external error identity replaced", e)
				}
			}
			if posts.Load() != 0 {
				t.Fatal("local/admission/I/O failure performed model work", posts.Load())
			}
			if !p.closed || p.Owner == nil || p.Controller == nil || p.Host == nil {
				t.Fatal("actual main lifecycle incomplete")
			}
		})
	}
}
