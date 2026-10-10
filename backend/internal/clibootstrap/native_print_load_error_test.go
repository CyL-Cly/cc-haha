package clibootstrap

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type loadErrorUnreadInput struct{ reads atomic.Int32 }

func (r *loadErrorUnreadInput) Read([]byte) (int, error) {
	r.reads.Add(1)
	return 0, errors.New("init consumed input")
}
func TestActualMainPrintResumeLoadErrorScopeAndOwnerClose(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(map[bool]string{false: "SDK-unbound", true: "public-print"}[public], func(t *testing.T) {
			n := nativeForTest(t)
			args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
			if e != nil {
				t.Fatal(e)
			}
			initial := args.SessionID
			args.SessionID = initial
			args.Resume = "20202020-2020-4020-8020-202020202020"
			args.ForkSession = true
			input := &loadErrorUnreadInput{}
			var out, stderr bytes.Buffer
			m, e := NewMainPrint(MainPrintOptions{PublicPrintLoadErrors: public, Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: &out, Error: &stderr})
			if e != nil {
				t.Fatal(e)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			e = m.StartSession(ctx)
			var load *PrintLoadError
			if public {
				if !errors.As(e, &load) || load.sessionID != initial || load.Error() != "No conversation found with session ID: "+args.Resume {
					t.Fatal("public typed initial SID/message", e, load)
				}
			} else if errors.As(e, &load) || e == nil || e.Error() != "No conversation found" {
				t.Fatal("SDK behavior changed", e)
			}
			if input.reads.Load() != 0 || m.initialized || m.Controller.SessionID() != initial || out.Len() != 0 {
				t.Fatal("resume failure admitted/restored state", input.reads.Load(), m.initialized, m.Controller.SessionID(), out.String())
			}
			closed := make(chan struct{})
			go func() { m.Close(); close(closed) }()
			select {
			case <-closed:
			case <-ctx.Done():
				t.Fatal("failed initialization Close hung")
			}
			if !m.closed {
				t.Fatal("owner was not closed")
			}
			_, e = m.Run(context.Background())
			if e == nil {
				t.Fatal("closed failed process ran")
			}
		})
	}
}
func TestActualMainPrintRejectsEmptyJSONLBeforeSwitchOnlyForPublicPrint(t *testing.T) {
	for _, public := range []bool{false, true} {
		t.Run(map[bool]string{false: "SDK-unbound", true: "public-print"}[public], func(t *testing.T) {
			n := nativeForTest(t)
			args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
			if e != nil {
				t.Fatal(e)
			}
			path := filepath.Join(n.CWD, "empty.JSONL")
			if e = os.WriteFile(path, nil, 0600); e != nil {
				t.Fatal(e)
			}
			args.Resume = path
			args.ForkSession = true
			m, e := NewMainPrint(MainPrintOptions{PublicPrintLoadErrors: public, Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: strings.NewReader(""), Output: io.Discard, Error: io.Discard})
			if e != nil {
				t.Fatal(e)
			}
			defer m.Close()
			e = m.StartSession(context.Background())
			var load *PrintLoadError
			if public {
				if !errors.As(e, &load) || !strings.HasPrefix(load.Error(), "No conversation found with session ID: ") || strings.HasSuffix(load.Error(), path) || m.initialized {
					t.Fatal("empty JSONL public error", e)
				}
			} else if e != nil || !m.initialized {
				t.Fatal("unbound SDK empty activation changed", e)
			}
		})
	}
}
func TestPublicPrintLoadErrorWriterDoesNotCaptureUnrelatedFailures(t *testing.T) {
	var out, stderr bytes.Buffer
	plain := errors.New("native provider startup failure")
	if handled, e := WritePrintLoadError(&out, &stderr, "stream-json", plain); handled || e != nil || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatal("unrelated error turned into load error", handled, e)
	}
}

func TestActualMainPrintRunPublicResumeFailureStopsBeforeInput(t *testing.T) {
	n := nativeForTest(t)
	args, e := ParsePrintArguments(mainRecipeArgs(), "fixture")
	if e != nil {
		t.Fatal(e)
	}
	args.Resume = "20202020-2020-4020-8020-202020202020"
	args.ForkSession = true
	input := &loadErrorUnreadInput{}
	var out, stderr bytes.Buffer
	m, e := NewMainPrint(MainPrintOptions{PublicPrintLoadErrors: true, Arguments: args, Native: n, PID: os.Getpid(), Version: "fixture", Platform: "darwin", Input: input, Output: &out, Error: &stderr})
	if e != nil {
		t.Fatal(e)
	}
	code, e := m.Run(context.Background())
	var load *PrintLoadError
	if code != 1 || !errors.As(e, &load) || !m.closed || input.reads.Load() != 0 || out.Len() != 0 || stderr.Len() != 0 {
		t.Fatal("direct print load branch consumed stdin/output or failed Close", code, e, m.closed, input.reads.Load(), out.String(), stderr.String())
	}
	if handled, e := WritePrintLoadError(&out, &stderr, "stream-json", e); !handled || e != nil || bytes.Count(out.Bytes(), []byte("\n")) != 1 || stderr.Len() != 0 {
		t.Fatal("direct public caller formatting", handled, e, out.String(), stderr.String())
	}
}
