package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/processoutput"
)

type viewError struct{ code, message string }

func (e *viewError) Error() string { return e.message }
func (e *viewError) Unwrap() error {
	if e.code == "EPIPE" {
		return syscall.EPIPE
	}
	return syscall.EIO
}

func run(home string) error {
	var input struct {
		Name           string `json:"name"`
		Classification string `json:"classification"`
	}
	data, err := os.ReadFile(filepath.Join(home, "input.json"))
	if err != nil {
		return err
	}
	if err = json.Unmarshal(data, &input); err != nil {
		return err
	}
	if !strings.HasPrefix(input.Name, "callback-") && !strings.HasPrefix(input.Name, "sync-error-") {
		return errors.New("only declared original callback adapter profiles")
	}
	trace := []map[string]any{}
	add := func(event string, fields map[string]any) { fields["event"] = event; trace = append(trace, fields) }
	m := processoutput.New()
	destroyed := false
	var callback func(error)
	var drain func()
	stream := &processoutput.Stream{Destroyed: func() bool { return destroyed }, Destroy: func() { destroyed = true }, OnceDrain: func(cb func()) { drain = cb }}
	stream.Write = func(data []byte, cb func(error)) (bool, error) {
		add("injected:write", map[string]any{"bytes": len(data)})
		if strings.HasPrefix(input.Name, "sync-error-") {
			code := strings.TrimPrefix(input.Name, "sync-error-")
			return false, &viewError{code, "original synchronous " + code}
		}
		callback = cb
		return false, nil
	}
	err = m.Write(stream, []byte("original-output\n"))
	var caught any
	if err != nil {
		e := err.(*viewError)
		caught = map[string]any{"name": "Error", "message": e.message, "code": e.code}
	}
	budget := func() int64 { return m.Budget().Milliseconds() }
	add("after-write", map[string]any{"budget": budget(), "caught": caught, "destroyed": destroyed})
	flushed := make(chan struct{})
	started := make(chan struct{})
	timeout := m.Budget()
	go func() {
		close(started)
		m.Flush(timeout)
		close(flushed)
	}()
	<-started
	if input.Name == "callback-backpressure" {
		callback(nil)
		returned := false
		select {
		case <-flushed:
			returned = true
		default:
		}
		add("after-success-callback", map[string]any{"flushed": returned, "budget": budget()})
		drain()
	} else if strings.HasPrefix(input.Name, "callback-error-") {
		code := strings.TrimPrefix(input.Name, "callback-error-")
		callback(&viewError{code, "original callback " + code})
	}
	<-flushed
	add("flush:settled", map[string]any{"budget": budget()})
	add("final", map[string]any{"budget": budget(), "destroyed": destroyed, "flushed": true})
	result := map[string]any{"name": input.Name, "classification": input.Classification, "trace": trace, "scope": "explicit callback/drain accounting adapter; no native physical stdout/full CLI", "processExitCode": 0}
	output, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(home, "result.json"), append(output, '\n'), 0600)
}

func main() {
	home := os.Getenv("HOME")
	base := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	real, err := filepath.EvalSymlinks(home)
	if err != nil || !filepath.IsAbs(home) || base == "" || real != home || !strings.HasPrefix(filepath.Base(home), "cc-haha-output-child-") || filepath.Dir(home) != base {
		panic("private disposable fixture HOME required")
	}
	if err := run(home); err != nil {
		panic(err)
	}
}
