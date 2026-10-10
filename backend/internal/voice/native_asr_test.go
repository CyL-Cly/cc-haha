package voice

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestActualSourceAndGoNativeWorkerTranscribeSameSpeech(t *testing.T) {
	root := os.Getenv("CC_HAHA_VOICE_NATIVE_ASR_ROOT")
	executable := os.Getenv("CC_HAHA_VOICE_NATIVE_ASR_EXECUTABLE")
	if root == "" || executable == "" {
		t.Skip("separate pinned local ASR evidence; no network in deterministic checks")
	}
	config := WorkerConfig{RuntimeRoot: filepath.Join(root, "native-runtime"), Model: filepath.Join(root, "model.int8.onnx"), Tokens: filepath.Join(root, "tokens.txt"), VAD: filepath.Join(root, "silero_vad.onnx"), Threads: 2, MaxAudioBytes: MaxAudioBytes, VADThreshold: 0.5, MinSilenceSeconds: 0.5, MinSpeechSeconds: 0.25, SegmentSeconds: 30}
	out := sourceVoice(t, "native-voice-native-asr.fixture.ts", map[string]any{"config": config, "wavPath": filepath.Join(root, "voice.wav")}, map[string]string{"CC_HAHA_VOICE_NATIVE_ASR_FIXTURE": "1"})
	var source struct{ Result Transcript }
	if e := json.Unmarshal(out, &source); e != nil {
		t.Fatal(e)
	}
	home := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	recognizer, e := NewRecognizer(RecognizerOptions{CWD: home, Environment: map[string]string{"HOME": home, "USERPROFILE": home, "TMPDIR": home, "PATH": "/usr/bin:/bin", "ANTHROPIC_API_KEY": "must-not-reach-worker", "HTTPS_PROXY": "http://must-not-reach-worker.invalid"}, Executable: executable, Config: func() (WorkerConfig, error) { return config, nil }})
	if e != nil {
		t.Fatal(e)
	}
	wav, e := os.ReadFile(filepath.Join(root, "voice.wav"))
	if e != nil {
		t.Fatal(e)
	}
	result, e := recognizer.Transcribe(ctx, wav, "en")
	if e != nil {
		t.Fatal(e)
	}
	if result.Text == "" || result.Text != source.Result.Text || result.AudioSeconds != source.Result.AudioSeconds {
		t.Fatalf("actual Source%s Go%s", source.Result.Text, result.Text)
	}
	if result.InferenceSeconds <= 0 {
		t.Fatal("inference accounting missing")
	}
	if e = recognizer.Close(ctx); e != nil {
		t.Fatal(e)
	}
	t.Logf("actual Source and Go=%q audioSeconds=%f", result.Text, result.AudioSeconds)
	files, e := os.ReadDir(home)
	if e != nil || len(files) != 0 {
		t.Fatal("worker touched private HOME", e, files)
	}
}
