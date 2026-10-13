package pdfread

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestNativePDFProcessTimeoutCancelAndCurrentEnvironment(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fixture process; native Windows acceptance is separate")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	executable := filepath.Join(bin, "pdfinfo")
	script := "#!/bin/sh\nprintf 'ACTUAL_PDF_NATIVE_STDOUT'; printf 'ACTUAL_PDF_NATIVE_STDERR' >&2; printf '%s' \"$PDF_ENV_MARKER\" > env.txt; printf 'STARTED' > started; /bin/sleep 30\n"
	if err := os.WriteFile(executable, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	environment := map[string]string{"HOME": home, "PATH": bin + ":/usr/bin:/bin", "PDF_ENV_MARKER": "first"}
	first := NativeRunner(environment, home)
	environment["PDF_ENV_MARKER"] = "changed-after-snapshot"
	result, err := first(context.Background(), "pdfinfo", nil, 120*time.Millisecond)
	if err != nil || result.Code != 1 {
		t.Fatal("real owned pre-start or active timeout lost original nonthrowing result", result, err)
	}
	data, err := os.ReadFile(filepath.Join(home, "env.txt"))
	if err == nil && string(data) != "first" {
		t.Fatal("runner did not capture immutable owner env", err, string(data))
	}
	if err := os.Remove(filepath.Join(home, "started")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	next := NativeRunner(environment, home)
	parent, cancel := context.WithCancelCause(context.Background())
	cause := errors.New("actual PDF owner closed")
	type completion struct {
		value   ProcessResult
		failure error
	}
	done := make(chan completion, 1)
	go func() { value, err := next(parent, "pdfinfo", nil, 30*time.Second); done <- completion{value, err} }()
	deadline := time.After(3 * time.Second)
	timer := time.NewTicker(5 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-timer.C:
			if _, err := os.Stat(filepath.Join(home, "started")); err == nil {
				cancel(cause)
				goto joined
			}
		case <-deadline:
			t.Fatal("native PDF process did not start")
		}
	}
joined:
	select {
	case receipt := <-done:
		if !errors.Is(receipt.failure, cause) || receipt.value.Stdout != "ACTUAL_PDF_NATIVE_STDOUT" || receipt.value.Stderr != "ACTUAL_PDF_NATIVE_STDERR" {
			t.Fatal("native cancel changed actual owner cause or lost emitted output", receipt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("native PDF process did not join after owner cancel")
	}
	data, err = os.ReadFile(filepath.Join(home, "env.txt"))
	if err != nil || string(data) != "changed-after-snapshot" {
		t.Fatal("new owner snapshot did not reach native executable", err, string(data))
	}
	for _, name := range []string{"unowned-command", "pdfinfo"} {
		result, err := NativeRunner(map[string]string{"HOME": home, "PATH": filepath.Join(home, "missing")}, home)(context.Background(), name, nil, time.Second)
		if name == "unowned-command" {
			if err == nil {
				t.Fatal("arbitrary PDF executable admitted")
			}
		} else if err != nil || result.Code != 1 || result.Stderr != "" || result.Stdout != "" {
			t.Fatal("Source absent executable contract", result, err)
		}
	}
}
func TestNativePDFOutputLimitCannotBypassWriterOrLosePartialBytes(t *testing.T) {
	canceled := false
	output := nativeOutput{limit: 4, cancel: func() { canceled = true }}
	if count, err := output.Write([]byte("ab")); err != nil || count != 2 {
		t.Fatal(count, err)
	}
	if count, err := output.Write([]byte("cde")); err == nil || count != 2 || !canceled || output.data.String() != "abcd" {
		t.Fatal("original process maxBuffer was not enforced", count, err, canceled, output.data.String())
	}
}
func TestNativePDFFileGuardRejectsEveryUnownedOperation(t *testing.T) {
	files := NativeFiles{}
	path := filepath.Join(t.TempDir(), "guarded.pdf")
	_, statErr := files.Stat(path)
	_, readErr := files.ReadFile(path)
	mkdirErr := files.MkdirAll(filepath.Dir(path), 0700)
	_, dirErr := files.ReadDir(filepath.Dir(path))
	for _, err := range []error{statErr, readErr, mkdirErr, dirErr} {
		if err == nil || !strings.Contains(err.Error(), "guard is not bound") {
			t.Fatal("unbound PDF path was accessed", err)
		}
	}
}
