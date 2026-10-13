package tui

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestRunCLIPrefillDraftWaitsForUserEnterAndUsesTerminalTitle(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	var submits atomic.Int32
	inputs := make(chan Input, 1)
	session := &testSession{submit: func(_ context.Context, input Input, _ Emit) error { submits.Add(1); inputs <- input; return nil }}
	options := testOptions(reader, &output)
	options.InitialDraft = "REVIEW_BEFORE_ENTER 中文"
	options.TerminalTitle = "NAMED_CLI_612"
	done := make(chan error, 1)
	go func() { done <- Run(ctx, session, options) }()
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(output.String(), options.InitialDraft) {
		if time.Now().After(deadline) {
			t.Fatal("prefill absent from actual renderer", output.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if submits.Load() != 0 {
		t.Fatal("prefill auto-submitted")
	}
	if _, e := writer.Write([]byte("\r")); e != nil {
		t.Fatal(e)
	}
	got := await(t, inputs)
	if got.Text != options.InitialDraft {
		t.Fatal("user Enter lost draft", got)
	}
	cancel()
	if e := await(t, done); e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	if submits.Load() != 1 {
		t.Fatal("duplicate submission", submits.Load())
	}
	if !strings.Contains(output.String(), "\x1b]2;NAMED_CLI_612") {
		t.Fatal("title did not reach terminal OSC renderer", output.String())
	}
}

func TestRunCLIPositionalPromptLeavesPrefillDraftEditable(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	var output lockedBuffer
	inputs := make(chan Input, 2)
	session := &testSession{submit: func(_ context.Context, input Input, _ Emit) error { inputs <- input; return nil }}
	options := testOptions(reader, &output)
	options.InitialPrompt = "RUN_SEPARATE_POSITIONAL"
	options.InitialDraft = "PREFILL_SURVIVES_INITIAL_MESSAGE"
	done := make(chan error, 1)
	go func() { done <- Run(ctx, session, options) }()
	if got := await(t, inputs); got.Text != options.InitialPrompt {
		t.Fatal("positional startup input changed", got)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(output.String(), options.InitialDraft) {
		if time.Now().After(deadline) {
			t.Fatal("initialMessage clearBuffer clobbered prefill", output.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	if _, e := writer.Write([]byte("\r")); e != nil {
		t.Fatal(e)
	}
	if got := await(t, inputs); got.Text != options.InitialDraft {
		t.Fatal("draft was not independently user submitted", got)
	}
	cancel()
	if e := await(t, done); e != nil && !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
}
