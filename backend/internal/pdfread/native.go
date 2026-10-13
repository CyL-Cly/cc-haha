package pdfread

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type NativeFiles struct{ Check func(string, string) error }

func (f NativeFiles) check(operation, path string) error {
	if f.Check == nil {
		return errors.New("PDF native path guard is not bound")
	}
	return f.Check(operation, path)
}
func (f NativeFiles) Stat(path string) (os.FileInfo, error) {
	if err := f.check("PDF stat", path); err != nil {
		return nil, err
	}
	return os.Stat(path)
}
func (f NativeFiles) ReadFile(path string) ([]byte, error) {
	if err := f.check("PDF read", path); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
}
func (f NativeFiles) MkdirAll(path string, mode os.FileMode) error {
	if err := f.check("PDF mkdir", path); err != nil {
		return err
	}
	return os.MkdirAll(path, mode)
}
func (f NativeFiles) ReadDir(path string) ([]os.DirEntry, error) {
	if err := f.check("PDF readdir", path); err != nil {
		return nil, err
	}
	return os.ReadDir(path)
}

// NativeRunner resolves only the requested Poppler executables using this
// process's original PATH. A missing executable has Source's nonthrowing exit.
func NativeRunner(environment map[string]string, cwd string) func(context.Context, string, []string, time.Duration) (ProcessResult, error) {
	snapshot := map[string]string{}
	for key, value := range environment {
		snapshot[key] = value
	}
	return func(parent context.Context, name string, args []string, timeout time.Duration) (ProcessResult, error) {
		if name != "pdfinfo" && name != "pdftoppm" {
			return ProcessResult{}, errors.New("unowned PDF executable")
		}
		path := findExecutable(name, snapshot["PATH"], snapshot["PATHEXT"])
		if path == "" {
			return ProcessResult{Code: 1}, nil
		}
		ctx, cancel := context.WithTimeout(parent, timeout)
		defer cancel()
		command := exec.CommandContext(ctx, path, args...)
		command.Dir = cwd
		keys := []string{}
		for key := range snapshot {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			command.Env = append(command.Env, key+"="+snapshot[key])
		}
		stdout := nativeOutput{limit: 100000000, cancel: cancel}
		stderr := nativeOutput{limit: 100000000, cancel: cancel}
		command.Stdout = &stdout
		command.Stderr = &stderr
		command.WaitDelay = 2 * time.Second
		configurePDFProcess(command, snapshot)
		err := command.Run()
		result := ProcessResult{Code: 0, Stdout: stdout.data.String(), Stderr: stderr.data.String()}
		if command.ProcessState != nil {
			result.Code = command.ProcessState.ExitCode()
		}
		if result.Code < 0 || (err != nil && result.Code == 0) {
			result.Code = 1
		}
		if context.Cause(parent) != nil {
			return result, context.Cause(parent)
		}
		// execFileNoThrow reports spawn/timeout/nonzero exit as data to PDF policy.
		if err != nil && command.ProcessState == nil {
			result.Code = 1
		}
		return result, nil
	}
}
func findExecutable(name, path, pathExt string) string {
	suffixes := []string{""}
	if isWindowsPDFProcess() {
		if pathExt == "" {
			pathExt = ".COM;.EXE;.BAT;.CMD"
		}
		for _, ext := range strings.Split(pathExt, ";") {
			if ext != "" {
				suffixes = append(suffixes, strings.ToLower(ext), strings.ToUpper(ext))
			}
		}
	}
	for _, dir := range filepath.SplitList(path) {
		if dir == "" {
			continue
		}
		for _, suffix := range suffixes {
			candidate := filepath.Join(dir, name+suffix)
			info, err := os.Stat(candidate)
			if err == nil && !info.IsDir() && (isWindowsPDFProcess() || info.Mode()&0111 != 0) {
				return candidate
			}
		}
	}
	return ""
}

// execa's original default permits 100MB per output stream. Stop the owned
// native process when that boundary is crossed rather than growing unbounded.
type nativeOutput struct {
	data   bytes.Buffer
	limit  int
	cancel context.CancelFunc
}

func (b *nativeOutput) Write(data []byte) (int, error) {
	if len(data) > b.limit-b.data.Len() {
		remaining := b.limit - b.data.Len()
		if remaining > 0 {
			_, _ = b.data.Write(data[:remaining])
		}
		b.cancel()
		return remaining, errors.New("PDF native process output exceeded original maxBuffer")
	}
	return b.data.Write(data)
}
