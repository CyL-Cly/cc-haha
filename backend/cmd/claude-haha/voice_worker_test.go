package main

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/voice"
)

func TestVoiceWorkerEntrypointPrecedesHarnessAndConsumesProcessCredentials(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", home)
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	t.Setenv("CLAUDE_CODE_FORCE_RECOVERY_CLI", "1")
	t.Setenv(voice.WorkerConfigEnv, "{}")
	t.Setenv(voice.WorkerTokenEnv, "private_fixture_worker_token_1234567890")
	oldOut, oldErr := os.Stdout, os.Stderr
	outR, outW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer outR.Close()
	errR, errW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer errR.Close()
	os.Stdout, os.Stderr = outW, errW
	defer func() { os.Stdout, os.Stderr = oldOut, oldErr }()
	code := run(context.Background(), []string{"--voice-worker"})
	_ = outW.Close()
	_ = errW.Close()
	out, _ := io.ReadAll(outR)
	stderr, _ := io.ReadAll(errR)
	if code != 1 || len(out) != 0 || !strings.Contains(string(stderr), "Voice worker config is missing runtimeRoot") || strings.Contains(string(stderr), "ANTHROPIC") {
		t.Fatalf("worker entered normal Agent/recovery: code=%d out=%q err=%q", code, out, stderr)
	}
	if _, present := os.LookupEnv(voice.WorkerTokenEnv); present {
		t.Fatal("worker credential remains in process environment")
	}
	if _, present := os.LookupEnv(voice.WorkerConfigEnv); present {
		t.Fatal("worker runtime configuration remains in process environment")
	}
	entries, err := os.ReadDir(home)
	if err != nil || len(entries) != 0 {
		t.Fatal("internal voice worker wrote normal CLI/provider state", entries, err)
	}
}
