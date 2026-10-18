package clibootstrap

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

type observedStdinRead struct {
	data    string
	failure error
}
type observedStdinPipe struct {
	file   *os.File
	mu     sync.Mutex
	reads  []observedStdinRead
	calls  int
	second chan struct{}
}

func (r *observedStdinPipe) Read(buffer []byte) (int, error) {
	r.mu.Lock()
	r.calls++
	if r.calls == 2 && r.second != nil {
		close(r.second)
	}
	r.mu.Unlock()
	count, failure := r.file.Read(buffer)
	r.mu.Lock()
	r.reads = append(r.reads, observedStdinRead{string(buffer[:count]), failure})
	r.mu.Unlock()
	return count, failure
}

// Read is a real private OS pipe, not a ready-line injection. The held partial
// packet proves that only newly needed physical reads leave the owner; complete
// buffered packets have no extra I/O read between their actual SDK ACKs.
func TestNativeSDKBufferedStdinRealPipeChunkingAndEOF(t *testing.T) {
	for _, profile := range []string{"one-chunk-multiple-lines", "split-half-line", "final-no-newline", "empty-eof"} {
		t.Run(profile, func(t *testing.T) {
			f := newNativePluginFixture(t)
			f.write(t, filepath.Join(f.config, "settings.json"), `{}`)
			f.n.Environment["ANTHROPIC_API_KEY"] = "offline-buffered-stdin"
			f.n.Environment["CLAUDE_CODE_SIMPLE"] = "1"
			f.n.Environment["CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC"] = "1"
			reader, writer, err := os.Pipe()
			if err != nil {
				t.Fatal(err)
			}
			defer reader.Close()
			defer writer.Close()
			first := `{"type":"control_request","request_id":"buffered-one","request":{"subtype":"get_settings"}}` + "\n"
			second := `{"type":"control_request","request_id":"buffered-two","request":{"subtype":"get_settings"}}` + "\n"
			payload := first + second
			if profile == "final-no-newline" {
				payload = strings.TrimSuffix(payload, "\n")
			}
			if profile == "empty-eof" {
				payload = ""
			}
			observed := &observedStdinPipe{file: reader}
			if profile == "split-half-line" {
				observed.second = make(chan struct{})
			}
			var output, diagnostic bytes.Buffer
			acks := []string{}
			readCountsAtACK := []int{}
			disabled := false
			print, err := NewMainPrint(MainPrintOptions{Arguments: PrintArguments{ComputerUse: &disabled, Model: "fixture-model", Tools: []string{""}, PermissionMode: "default", MaxTurns: 1, Sources: DefaultState()}, Native: f.n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: observed, Output: &output, Error: &diagnostic, OnOutput: func(value []byte) error {
				if strings.Contains(string(value), `"type":"control_response"`) {
					acks = append(acks, string(value))
					observed.mu.Lock()
					readCountsAtACK = append(readCountsAtACK, observed.calls)
					observed.mu.Unlock()
				}
				return nil
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer print.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			done := make(chan error, 1)
			go func() {
				code, err := print.Run(ctx)
				if err == nil && code != 0 {
					err = io.ErrUnexpectedEOF
				}
				done <- err
			}()
			if profile == "split-half-line" {
				cut := len(first) / 2
				if _, err := io.WriteString(writer, payload[:cut]); err != nil {
					t.Fatal(err)
				}
				select {
				case <-observed.second:
				case <-ctx.Done():
					t.Fatal("partial-line physical read did not start", ctx.Err())
				}
				// This callback must run while the second physical pipe read is
				// blocked. A blocking Peek/Read on Owner would deadlock here.
				receipt, err := print.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(*cooperative.Scope) error { return nil })
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-receipt.Done():
					if err := receipt.Wait(); err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("stdin partial read blocked Owner", ctx.Err())
				}
				if _, err := io.WriteString(writer, payload[cut:]); err != nil {
					t.Fatal(err)
				}
			} else if _, err := io.WriteString(writer, payload); err != nil {
				t.Fatal(err)
			}
			if err := writer.Close(); err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err, diagnostic.String())
				}
			case <-ctx.Done():
				t.Fatal("stdin EOF did not join", ctx.Err())
			}
			want := 2
			if profile == "empty-eof" {
				want = 0
			}
			if len(acks) != want {
				t.Fatalf("actual control ACKs=%d want=%d: %v", len(acks), want, acks)
			}
			if want > 0 && (!strings.Contains(acks[0], "buffered-one") || !strings.Contains(acks[1], "buffered-two")) {
				t.Fatalf("ACK order changed: %v", acks)
			}
			if profile == "one-chunk-multiple-lines" && (readCountsAtACK[0] != 1 || readCountsAtACK[1] != 1) {
				t.Fatalf("buffered complete line performed new physical read: %v", readCountsAtACK)
			}
			observed.mu.Lock()
			var bytesRead strings.Builder
			for _, read := range observed.reads {
				bytesRead.WriteString(read.data)
			}
			calls := observed.calls
			observed.mu.Unlock()
			if bytesRead.String() != payload {
				t.Fatalf("physical bytes changed: %q", bytesRead.String())
			}
			if profile == "final-no-newline" && calls != 2 {
				t.Fatalf("known EOF performed an extra physical read: %d", calls)
			}
			if profile == "empty-eof" && calls != 1 {
				t.Fatalf("empty EOF repeated physical read: %d", calls)
			}
			select {
			case <-print.Owner.Done():
			default:
				t.Fatal("native EOF left Owner active")
			}
		})
	}
}
