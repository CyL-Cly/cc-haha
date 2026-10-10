package voice

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

const WorkerConfigEnv = "CC_HAHA_VOICE_WORKER_CONFIG"
const WorkerTokenEnv = "CC_HAHA_VOICE_WORKER_TOKEN"

type WorkerConfig struct {
	RuntimeRoot       string  `json:"runtimeRoot"`
	Model             string  `json:"model"`
	Tokens            string  `json:"tokens"`
	VAD               string  `json:"vad"`
	Threads           int     `json:"threads"`
	MaxAudioBytes     int     `json:"maxAudioBytes"`
	VADThreshold      float64 `json:"vadThreshold"`
	MinSilenceSeconds float64 `json:"minSilenceSeconds"`
	MinSpeechSeconds  float64 `json:"minSpeechSeconds"`
	SegmentSeconds    float64 `json:"segmentSeconds"`
}
type workerInputError struct{ Message string }

func (e *workerInputError) Error() string { return e.Message }
func ReadWorkerConfig(environment map[string]string) (WorkerConfig, string, error) {
	raw, token := environment[WorkerConfigEnv], environment[WorkerTokenEnv]
	delete(environment, WorkerConfigEnv)
	delete(environment, WorkerTokenEnv)
	var config WorkerConfig
	if raw == "" || token == "" || !regexp.MustCompile(`^[A-Za-z0-9_-]{32,}$`).MatchString(token) {
		return config, "", fmt.Errorf("Voice worker was started without its configuration")
	}
	if e := json.Unmarshal([]byte(raw), &config); e != nil {
		return config, "", e
	}
	for _, field := range []struct{ Name, Value string }{{"runtimeRoot", config.RuntimeRoot}, {"model", config.Model}, {"tokens", config.Tokens}, {"vad", config.VAD}} {
		if field.Value == "" {
			return config, "", fmt.Errorf("Voice worker config is missing %s", field.Name)
		}
	}
	return config, token, nil
}
func WorkerHandler(token string, maxBytes int, transcribe func([]byte, string) (Transcript, error)) http.Handler {
	expected := []byte("Bearer " + token)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reply := func(status int, value any) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(value)
		}
		if subtle.ConstantTimeCompare([]byte(r.Header.Get("Authorization")), expected) != 1 {
			reply(401, map[string]string{"error": "Unauthorized"})
			return
		}
		if r.Method != "POST" || r.URL.Path != "/transcribe" {
			reply(404, map[string]string{"error": "Unknown endpoint"})
			return
		}
		length, e := strconv.ParseInt(r.Header.Get("Content-Length"), 10, 64)
		if e != nil || length < 46 || length > int64(maxBytes) {
			reply(413, map[string]string{"error": "Invalid speech audio size", "code": "invalid-input"})
			return
		}
		wav, e := io.ReadAll(r.Body)
		if e != nil {
			reply(500, map[string]string{"error": e.Error()})
			return
		}
		language := "auto"
		if values, ok := r.URL.Query()["language"]; ok {
			language = values[0]
		}
		result, e := transcribe(wav, language)
		if e != nil {
			body := map[string]string{"error": e.Error()}
			status := 500
			if _, ok := e.(*workerInputError); ok {
				body["code"] = "invalid-input"
				status = 400
			}
			reply(status, body)
			return
		}
		reply(200, result)
	})
}
func RunWorker(ctx context.Context, environment map[string]string, input io.Reader, output io.Writer) error {
	config, token, e := ReadWorkerConfig(environment)
	if e != nil {
		return e
	}
	transcriber, e := NewNativeTranscriber(config)
	if e != nil {
		return e
	}
	defer transcriber.Close()
	listener, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		return e
	}
	server := &http.Server{Handler: WorkerHandler(token, config.MaxAudioBytes, transcriber.Transcribe)}
	served := make(chan error, 1)
	go func() { served <- server.Serve(listener) }()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	if _, e = fmt.Fprintf(output, "{\"port\":%s}\n", port); e != nil {
		_ = server.Close()
		return e
	}
	stdinDone := make(chan struct{})
	go func() { _, _ = io.Copy(io.Discard, input); close(stdinDone) }()
	select {
	case <-ctx.Done():
	case <-stdinDone:
	case e = <-served:
		if !strings.Contains(e.Error(), "Server closed") {
			return e
		}
	}
	_ = server.Close()
	return nil
}
