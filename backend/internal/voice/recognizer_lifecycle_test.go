package voice

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func privateRecognizer(t *testing.T, home string, o RecognizerOptions) *Recognizer {
	t.Helper()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("Source transport fixture Bun")
	}
	worker, _ := filepath.Abs("../../../src/server/services/voice/sensevoice/__fixtures__/fakeWorker.mjs")
	o.CWD = home
	o.Environment = map[string]string{"HOME": home, "USERPROFILE": home, "TMPDIR": home, "PATH": "/usr/bin:/bin"}
	o.Config = func() (WorkerConfig, error) {
		return WorkerConfig{Model: filepath.Join(home, "model.onnx"), Tokens: filepath.Join(home, "tokens.txt"), VAD: filepath.Join(home, "vad.onnx"), RuntimeRoot: home}, nil
	}
	o.Spawn = func(cwd string, env map[string]string) (*exec.Cmd, error) {
		cmd := exec.Command(bun, "--no-env-file", worker)
		cmd.Dir = cwd
		cmd.Env = environmentList(env)
		return cmd, nil
	}
	r, e := NewRecognizer(o)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if e := r.Close(ctx); e != nil {
			t.Error(e)
		}
	})
	return r
}
func awaitVoice(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("voice transition timeout")
		}
		time.Sleep(time.Millisecond)
	}
}
func TestVoiceWorkerCancellationQueuePressureAndIdleCleanup(t *testing.T) {
	home := t.TempDir()
	idle := 30 * time.Millisecond
	max := 2
	r := privateRecognizer(t, home, RecognizerOptions{IdleTimeout: &idle, MaxPending: &max})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, e := r.Transcribe(ctx, make([]byte, 64), "hold"); first <- e }()
	awaitVoice(t, func() bool {
		data, _ := os.ReadFile(filepath.Join(home, "requests.log"))
		return strings.Contains(string(data), "hold")
	})
	queued := make(chan error, 1)
	go func() { _, e := r.Transcribe(context.Background(), make([]byte, 64), "en"); queued <- e }()
	awaitVoice(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.pending == 2 })
	if _, e := r.Transcribe(context.Background(), nil, "zh"); e == nil || e.Error() != "Speech transcription queue is full" {
		t.Fatal("missing queue pressure", e)
	}
	cancel()
	select {
	case e := <-first:
		if !errors.Is(e, context.Canceled) {
			t.Fatal("cancellation cause", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancel did not end in-flight worker")
	}
	select {
	case e := <-queued:
		if e != nil {
			t.Fatal("queued request did not recover", e)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("queued request stuck behind canceled worker")
	}
	awaitVoice(t, func() bool { r.mu.Lock(); defer r.mu.Unlock(); return r.worker == nil })
	starts, _ := os.ReadFile(filepath.Join(home, "starts.log"))
	if len(strings.Fields(string(starts))) != 2 {
		t.Fatal("canceled/idle workers not recycled", string(starts))
	}
	if e := r.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	if _, e := r.Transcribe(context.Background(), nil, "en"); e == nil || e.Error() != "Speech recognizer disposed" {
		t.Fatal("disposed recognizer accepted work", e)
	}
}
