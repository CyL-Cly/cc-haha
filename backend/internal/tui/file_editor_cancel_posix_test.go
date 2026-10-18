//go:build !windows

package tui

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestActualTerminalFileEditorCallerCancelJoinsPhysicalEditorAndNoLateWrite(t *testing.T) {
	home := t.TempDir()
	path := filepath.Join(home, "plan.md")
	fifo := filepath.Join(home, "release.fifo")
	if err := os.WriteFile(path, []byte("original"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	release, err := os.OpenFile(fifo, os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer release.Close()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	editor := filepath.Join(home, "owned-editor")
	script := "#!/bin/sh\nexec '" + strings.ReplaceAll(binary, "'", "'\"'\"'") + "' -test.run=^TestActualTerminalHeldFileEditorFixtureProcess$ -test.count=1 -- \"$@\"\n"
	if err = os.WriteFile(editor, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stdout, writer := io.Pipe()
	defer stdout.Close()
	defer writer.Close()
	execution := &fileEditorExecution{ctx: ctx, request: FileEditorRequest{Path: path, Editor: "./owned-editor", Directory: home, Environment: []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "PATH=/usr/bin:/bin", "CC_HAHA_HELD_EDITOR=1", "CC_HAHA_EDITOR_RELEASE=" + fifo}}, stdin: strings.NewReader(""), stdout: writer, stderr: io.Discard}
	done := make(chan error, 1)
	go func() { done <- execution.Run() }()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	pid := 0
	select {
	case line := <-ready:
		pid, err = strconv.Atoi(strings.TrimSpace(line))
		if err != nil || pid <= 0 {
			t.Fatal("actual editor process not reached", line, err)
		}
	case <-time.After(3 * time.Second):
		cancel()
		_, _ = release.Write([]byte("x"))
		t.Fatal("private editor startup stalled")
	}
	cancel()
	select {
	case err = <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal("caller cause lost", err)
		}
	case <-time.After(3 * time.Second):
		_, _ = release.Write([]byte("x"))
		<-done
		t.Fatal("cancellation left actual editor/process output alive")
	}
	if err = syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		_, _ = release.Write([]byte("x"))
		t.Fatal("actual editor not reaped", pid, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "original" {
		t.Fatal("cancelled editor made late write", string(data), err)
	}
}

func TestActualTerminalHeldFileEditorFixtureProcess(t *testing.T) {
	if os.Getenv("CC_HAHA_HELD_EDITOR") != "1" {
		t.Skip("owned actual held editor child")
	}
	path := os.Args[len(os.Args)-1]
	home := os.Getenv("HOME")
	if filepath.Dir(path) != home {
		t.Fatal("private editor path ownership")
	}
	reader, err := os.Open(os.Getenv("CC_HAHA_EDITOR_RELEASE"))
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	fmt.Println(os.Getpid())
	var buffer [1]byte
	if _, err = reader.Read(buffer[:]); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("LATE_EDITOR_WRITE"), 0600); err != nil {
		t.Fatal(err)
	}
}
