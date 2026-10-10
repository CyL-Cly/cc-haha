package voice

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf16"
)

type SpawnWorker func(string, map[string]string) (*exec.Cmd, error)
type RecognizerOptions struct {
	Config                                                   func() (WorkerConfig, error)
	CWD                                                      string
	Environment                                              map[string]string
	Executable                                               string
	Spawn                                                    SpawnWorker
	Random                                                   io.Reader
	IdleTimeout, StartupTimeout, InferenceTimeout, KillGrace *time.Duration
	MaxPending                                               *int
}
type runningWorker struct {
	cmd       *exec.Cmd
	port      int
	token     string
	stdin     io.WriteCloser
	done      chan struct{}
	mu        sync.Mutex
	stderr    string
	exitError error
	closed    bool
}
type Recognizer struct {
	o       RecognizerOptions
	mu      sync.Mutex
	tail    chan struct{}
	pending int
	worker  *runningWorker
	idle    *time.Timer
	ctx     context.Context
	cancel  context.CancelCauseFunc
	closed  bool
}

func duration(value *time.Duration, fallback time.Duration) time.Duration {
	if value != nil {
		return *value
	}
	return fallback
}
func NewRecognizer(o RecognizerOptions) (*Recognizer, error) {
	if o.Config == nil || !filepath.IsAbs(o.CWD) {
		return nil, errors.New("speech recognizer requires its owned asset/config producer")
	}
	if o.Random == nil {
		o.Random = rand.Reader
	}
	if o.Spawn == nil {
		o.Spawn = func(cwd string, env map[string]string) (*exec.Cmd, error) {
			executable := o.Executable
			if executable == "" {
				var e error
				executable, e = os.Executable()
				if e != nil {
					return nil, e
				}
			}
			executable, e := filepath.EvalSymlinks(executable)
			if e != nil {
				return nil, e
			}
			cmd := exec.Command(executable, "--voice-worker")
			cmd.Dir = cwd
			cmd.Env = environmentList(env)
			return cmd, nil
		}
	}
	ctx, cancel := context.WithCancelCause(context.Background())
	tail := make(chan struct{})
	close(tail)
	return &Recognizer{o: o, tail: tail, ctx: ctx, cancel: cancel}, nil
}
func WorkerEnvironment(environment, extra map[string]string) map[string]string {
	out := map[string]string{}
	for _, key := range []string{"PATH", "HOME", "USERPROFILE", "TMPDIR", "TMP", "TEMP", "SystemRoot", "SYSTEMROOT", "LANG", "LC_ALL", "LD_LIBRARY_PATH"} {
		if value := environment[key]; value != "" {
			out[key] = value
		}
	}
	out["BUN_OPTIONS"] = "--no-env-file"
	for key, value := range extra {
		out[key] = value
	}
	return out
}
func (r *Recognizer) Transcribe(ctx context.Context, wav []byte, language string) (Transcript, error) {
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Transcript{}, fmt.Errorf("Speech recognizer disposed")
	}
	if ctx.Err() != nil {
		r.mu.Unlock()
		return Transcript{}, context.Cause(ctx)
	}
	max := 4
	if r.o.MaxPending != nil {
		max = *r.o.MaxPending
	}
	if r.pending >= max {
		r.mu.Unlock()
		return Transcript{}, failure("voice/failed", "Speech transcription queue is full")
	}
	if r.idle != nil {
		r.idle.Stop()
	}
	previous := r.tail
	done := make(chan struct{})
	r.tail = done
	r.pending++
	r.mu.Unlock()
	defer func() {
		r.mu.Lock()
		r.pending--
		if r.pending == 0 && !r.closed && duration(r.o.IdleTimeout, 5*time.Minute) > 0 {
			r.idle = time.AfterFunc(duration(r.o.IdleTimeout, 5*time.Minute), func() {
				r.mu.Lock()
				idle := r.pending == 0 && !r.closed
				r.mu.Unlock()
				if idle {
					_ = r.StopWorker(context.Background())
				}
			})
		}
		close(done)
		r.mu.Unlock()
	}()
	<-previous
	combined, cancel := context.WithCancelCause(ctx)
	stop := context.AfterFunc(r.ctx, func() { cancel(context.Cause(r.ctx)) })
	defer stop()
	defer cancel(nil)
	if combined.Err() != nil {
		return Transcript{}, context.Cause(combined)
	}
	worker, e := r.ensure(combined)
	if e != nil {
		_ = r.StopWorker(context.Background())
		if combined.Err() != nil {
			return Transcript{}, context.Cause(combined)
		}
		return Transcript{}, failure("voice/failed", "Speech recognizer could not start: "+e.Error())
	}
	callCtx, cancelCall := context.WithTimeoutCause(combined, duration(r.o.InferenceTimeout, 2*time.Minute), fmt.Errorf("Speech recognition timed out"))
	defer cancelCall()
	result, e := postWAV(callCtx, worker, wav, language)
	if e == nil {
		return result, nil
	}
	var invalid *workerInputError
	if errors.As(e, &invalid) {
		return Transcript{}, failure("voice/invalid-audio", invalid.Message)
	}
	_ = r.StopWorker(context.Background())
	if combined.Err() != nil {
		return Transcript{}, context.Cause(combined)
	}
	worker.mu.Lock()
	stderr := strings.TrimSpace(worker.stderr)
	worker.mu.Unlock()
	tail := ""
	if stderr != "" {
		tail = " (" + tailText(stderr, 300) + ")"
	}
	return Transcript{}, failure("voice/failed", "Speech recognizer failed: "+e.Error()+tail)
}
func (r *Recognizer) ensure(ctx context.Context) (*runningWorker, error) {
	r.mu.Lock()
	old := r.worker
	r.mu.Unlock()
	if old != nil {
		old.mu.Lock()
		closed := old.closed
		old.mu.Unlock()
		if !closed {
			return old, nil
		}
		_ = r.StopWorker(context.Background())
	}
	config, e := r.o.Config()
	if e != nil {
		return nil, e
	}
	random := make([]byte, 32)
	if _, e = io.ReadFull(r.o.Random, random); e != nil {
		return nil, e
	}
	token := hex.EncodeToString(random)
	data, e := json.Marshal(config)
	if e != nil {
		return nil, e
	}
	cmd, e := r.o.Spawn(r.o.CWD, WorkerEnvironment(r.o.Environment, map[string]string{WorkerTokenEnv: token, WorkerConfigEnv: string(data)}))
	if e != nil {
		return nil, e
	}
	stdout, e := cmd.StdoutPipe()
	if e != nil {
		return nil, e
	}
	stderr, e := cmd.StderrPipe()
	if e != nil {
		return nil, e
	}
	stdin, e := cmd.StdinPipe()
	if e != nil {
		return nil, e
	}
	if e = cmd.Start(); e != nil {
		_ = stdin.Close()
		return nil, e
	}
	worker := &runningWorker{cmd: cmd, token: token, stdin: stdin, done: make(chan struct{})}
	r.mu.Lock()
	r.worker = worker
	r.mu.Unlock()
	stderrDone := make(chan struct{})
	go func() {
		defer close(stderrDone)
		reader := bufio.NewReader(stderr)
		buffer := make([]byte, 1024)
		for {
			n, e := reader.Read(buffer)
			if n > 0 {
				worker.mu.Lock()
				worker.stderr = tailText(worker.stderr+string(buffer[:n]), 4096)
				worker.mu.Unlock()
			}
			if e != nil {
				return
			}
		}
	}()
	go func() {
		e := cmd.Wait()
		<-stderrDone
		worker.mu.Lock()
		worker.closed = true
		worker.exitError = e
		worker.mu.Unlock()
		close(worker.done)
	}()
	type readiness struct {
		port int
		err  error
	}
	ready := make(chan readiness, 1)
	go func() {
		port, e := readPort(stdout)
		ready <- readiness{port, e}
		if e == nil {
			_, _ = io.Copy(io.Discard, stdout)
		}
	}()
	startup, cancel := context.WithTimeoutCause(ctx, duration(r.o.StartupTimeout, time.Minute), fmt.Errorf("Speech recognizer startup timed out"))
	defer cancel()
	select {
	case value := <-ready:
		if value.err != nil {
			if errors.Is(value.err, io.EOF) {
				select {
				case <-worker.done:
					return nil, workerExitFailure(worker)
				case <-startup.Done():
					return nil, context.Cause(startup)
				}
			}
			return nil, value.err
		}
		worker.port = value.port
		return worker, nil
	case <-worker.done:
		return nil, workerExitFailure(worker)
	case <-startup.Done():
		return nil, context.Cause(startup)
	}
}
func readPort(reader io.Reader) (int, error) {
	buffer := []byte{}
	scratch := make([]byte, 256)
	for {
		n, e := reader.Read(scratch)
		buffer = append(buffer, scratch[:n]...)
		if len(buffer) > 4096 {
			return 0, fmt.Errorf("Speech worker readiness line is too long")
		}
		if end := bytes.IndexByte(buffer, '\n'); end >= 0 {
			var value struct {
				Port json.RawMessage `json:"port"`
			}
			if json.Unmarshal(buffer[:end], &value) != nil {
				return 0, fmt.Errorf("Speech worker printed an invalid readiness line")
			}
			var number float64
			if json.Unmarshal(value.Port, &number) == nil && number > 0 && number < 65536 && number == float64(int(number)) {
				return int(number), nil
			}
			return 0, fmt.Errorf("Speech worker reported an invalid port")
		}
		if e != nil {
			return 0, e
		}
	}
}
func postWAV(ctx context.Context, worker *runningWorker, wav []byte, language string) (Transcript, error) {
	req, e := http.NewRequestWithContext(ctx, "POST", fmt.Sprintf("http://127.0.0.1:%d/transcribe?language=%s", worker.port, url.QueryEscape(language)), bytes.NewReader(wav))
	if e != nil {
		return Transcript{}, e
	}
	req.Header.Set("Authorization", "Bearer "+worker.token)
	req.Header.Set("Content-Type", "audio/wav")
	client := &http.Client{Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}}
	res, e := client.Do(req)
	if e != nil {
		if ctx.Err() != nil {
			return Transcript{}, context.Cause(ctx)
		}
		return Transcript{}, e
	}
	defer res.Body.Close()
	body, e := io.ReadAll(io.LimitReader(res.Body, 128*1024+1))
	if e != nil {
		return Transcript{}, e
	}
	if len(body) > 128*1024 {
		return Transcript{}, fmt.Errorf("Speech worker response exceeded its size limit")
	}
	value := object(body)
	if !value.IsObject() {
		return Transcript{}, fmt.Errorf("Speech worker returned malformed output")
	}
	if res.StatusCode == 200 {
		var result Transcript
		var text string
		var audio, inference float64
		if !isManifestString(value.Get("text")) || !isManifestNumber(value.Get("audioSeconds")) || !isManifestNumber(value.Get("inferenceSeconds")) || json.Unmarshal(value.Get("text"), &text) != nil || json.Unmarshal(value.Get("audioSeconds"), &audio) != nil || json.Unmarshal(value.Get("inferenceSeconds"), &inference) != nil {
			return result, fmt.Errorf("Speech worker returned an unexpected response")
		}
		return Transcript{text, audio, inference}, nil
	}
	message := value.String("error")
	if value.Get("error") == nil {
		message = fmt.Sprintf("Speech worker responded %d", res.StatusCode)
	}
	if value.String("code") == "invalid-input" && (res.StatusCode == 400 || res.StatusCode == 413) {
		return Transcript{}, &workerInputError{message}
	}
	return Transcript{}, fmt.Errorf("%s", message)
}
func (r *Recognizer) StopWorker(ctx context.Context) error {
	r.mu.Lock()
	worker := r.worker
	r.worker = nil
	r.mu.Unlock()
	if worker == nil {
		return nil
	}
	worker.mu.Lock()
	closed := worker.closed
	worker.closed = true
	worker.mu.Unlock()
	if closed {
		return nil
	}
	_ = worker.cmd.Process.Signal(syscall.SIGTERM)
	timer := time.AfterFunc(duration(r.o.KillGrace, time.Second), func() { _ = worker.cmd.Process.Kill() })
	defer timer.Stop()
	select {
	case <-worker.done:
		_ = worker.stdin.Close()
		return nil
	case <-ctx.Done():
		_ = worker.cmd.Process.Kill()
		return context.Cause(ctx)
	}
}
func (r *Recognizer) Close(ctx context.Context) error {
	r.mu.Lock()
	r.closed = true
	if r.idle != nil {
		r.idle.Stop()
	}
	r.cancel(fmt.Errorf("Speech recognizer disposed"))
	tail := r.tail
	r.mu.Unlock()
	select {
	case <-tail:
		return r.StopWorker(ctx)
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}
func tailText(value string, limit int) string {
	units := utf16.Encode([]rune(value))
	if len(units) > limit {
		units = units[len(units)-limit:]
	}
	return string(utf16.Decode(units))
}

func workerExitFailure(worker *runningWorker) error {
	worker.mu.Lock()
	defer worker.mu.Unlock()
	code := 0
	if worker.cmd.ProcessState != nil {
		code = worker.cmd.ProcessState.ExitCode()
	}
	tail := strings.TrimSpace(worker.stderr)
	if tail != "" {
		return fmt.Errorf("worker exited (%d) %s", code, tailText(tail, 300))
	}
	return fmt.Errorf("worker exited (%d)", code)
}
