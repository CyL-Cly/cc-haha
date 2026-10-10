package voice

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRecognizerRealProcessesAndHTTPMatchActualSourceLifecycle(t *testing.T) {
	languages := []string{"zh", "en", "invalid-input", "zh", "error", "zh", "garbage", "zh", "hold", "zh", "crash", "zh"}
	out := sourceVoice(t, "native-voice-recognizer.fixture.ts", map[string]any{"languages": languages}, map[string]string{"CC_HAHA_VOICE_RECOGNIZER_FIXTURE": "1"})
	type row struct {
		Result  *Transcript `json:"result,omitempty"`
		Message string      `json:"message,omitempty"`
		Code    string      `json:"code,omitempty"`
		Status  int         `json:"status,omitempty"`
	}
	var source struct {
		Rows     []row
		Starts   int
		Requests []string
	}
	if e := json.Unmarshal(out, &source); e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	worker, _ := filepath.Abs("../../../src/server/services/voice/sensevoice/__fixtures__/fakeWorker.mjs")
	idle := time.Duration(0)
	inference := 60 * time.Millisecond
	grace := 100 * time.Millisecond
	recognizer, e := NewRecognizer(RecognizerOptions{CWD: home, Environment: map[string]string{"HOME": home, "USERPROFILE": home, "TMPDIR": home, "PATH": "/usr/bin:/bin", "ANTHROPIC_AUTH_TOKEN": "must-not-inherit", "HTTP_PROXY": "http://must-not-inherit.invalid"}, Config: func() (WorkerConfig, error) {
		return WorkerConfig{RuntimeRoot: home, Model: filepath.Join(home, "model.onnx"), Tokens: filepath.Join(home, "tokens.txt"), VAD: filepath.Join(home, "vad.onnx"), Threads: 1, MaxAudioBytes: 1024, VADThreshold: 0.5, MinSilenceSeconds: 0.5, MinSpeechSeconds: 0.25, SegmentSeconds: 30}, nil
	}, IdleTimeout: &idle, InferenceTimeout: &inference, KillGrace: &grace, Spawn: func(cwd string, env map[string]string) (*exec.Cmd, error) {
		if env["ANTHROPIC_AUTH_TOKEN"] != "" || env["HTTP_PROXY"] != "" {
			t.Error("worker inherited host secrets/proxy")
		}
		if len(env[WorkerTokenEnv]) != 64 {
			t.Error("worker authority entropy")
		}
		cmd := exec.Command(bun, "--no-env-file", worker)
		cmd.Dir = cwd
		cmd.Env = environmentList(env)
		return cmd, nil
	}})
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	actual := []row{}
	for _, language := range languages {
		result, e := recognizer.Transcribe(ctx, make([]byte, 64), language)
		if e == nil {
			actual = append(actual, row{Result: &result})
			continue
		}
		entry := row{Message: e.Error()}
		if own, ok := e.(*Error); ok {
			entry.Code = own.Code
			entry.Status = own.Status
		}
		actual = append(actual, entry)
	}
	if e = recognizer.Close(ctx); e != nil {
		t.Fatal(e)
	}
	for i, row := range actual {
		expected := source.Rows[i]
		if languages[i] == "crash" {
			if row.Code != expected.Code || row.Status != expected.Status {
				t.Errorf("crash classification Source%+v Go%+v", expected, row)
			}
			continue
		}
		compareVoice(t, "request "+languages[i], row, expected)
	}
	starts, _ := os.ReadFile(filepath.Join(home, "starts.log"))
	count := len(strings.Fields(string(starts)))
	if count != source.Starts {
		t.Errorf("worker restart Source%d Go%d", source.Starts, count)
	}
	requests, _ := os.ReadFile(filepath.Join(home, "requests.log"))
	recorded := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(requests)), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 1 {
			recorded = append(recorded, fields[1])
		}
	}
	compareVoice(t, "serialized real worker requests", recorded, source.Requests)
}
