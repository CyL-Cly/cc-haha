package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"

	tea "charm.land/bubbletea/v2"
	"github.com/NanmiCoder/cc-haha/backend/internal/terminaleditor"
)

// FileEditorRequest is a terminal rendering handoff, not a new tool or history
// producer. Its caller retains the original input's context and completion.
type FileEditorRequest struct {
	Context      context.Context
	Path, Editor string
	Directory    string
	Environment  []string
	Reply        chan FileEditorResult
}
type FileEditorResult struct {
	Error   string
	Failure error
}

type fileEditorExecution struct {
	ctx            context.Context
	request        FileEditorRequest
	result         FileEditorResult
	stdin          io.Reader
	stdout, stderr io.Writer
}

func (e *fileEditorExecution) SetStdin(input io.Reader) {
	if e.stdin == nil {
		e.stdin = input
	}
}
func (e *fileEditorExecution) SetStdout(output io.Writer) {
	if e.stdout == nil {
		e.stdout = output
	}
}
func (e *fileEditorExecution) SetStderr(output io.Writer) {
	if e.stderr == nil {
		e.stderr = output
	}
}

func (e *fileEditorExecution) Run() error {
	if e.request.Editor == "" {
		return nil
	}
	physical := e.request.Path
	if !filepath.IsAbs(physical) {
		physical = filepath.Join(e.request.Directory, physical)
	}
	if _, err := os.Stat(physical); err != nil {
		return nil
	}
	command := terminaleditor.Command(e.request.Editor, e.request.Path)
	shell, args := "/bin/sh", []string{"-c", command}
	if runtime.GOOS == "windows" {
		shell = "cmd.exe"
		args = []string{"/d", "/s", "/c", command}
	}
	child := exec.CommandContext(e.ctx, shell, args...)
	child.Dir = e.request.Directory
	child.Env = append([]string{}, e.request.Environment...)
	child.Stdin, child.Stdout, child.Stderr = e.stdin, e.stdout, e.stderr
	joined := configureFileEditorProcess(child)
	err := child.Run()
	if e.ctx.Err() != nil {
		e.result.Failure = errors.Join(context.Cause(e.ctx), joined())
		return e.result.Failure
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() != 0 {
		e.result.Error = terminaleditor.DisplayName(e.request.Editor) + " exited with code " + strconv.Itoa(exit.ExitCode())
	}
	// Source returns null on an unavailable editor/read-back error. A numeric
	// nonzero process status is its only user-visible editor failure.
	if err == nil {
		_, _ = os.ReadFile(physical)
	}
	return nil
}

func (m *terminalModel) terminalFileEditorCommand(event Event) tea.Cmd {
	if event.FileEditor == nil {
		return nil
	}
	ctx := event.FileEditor.Context
	if ctx == nil {
		ctx = m.ctx
	}
	execution := &fileEditorExecution{ctx: ctx, request: *event.FileEditor, stdin: m.options.Input, stdout: m.options.Output, stderr: m.options.Output}
	m.state.Editing = true
	return tea.Exec(execution, func(err error) tea.Msg {
		if err != nil {
			execution.result.Failure = err
		}
		return fileEditorReturnedMsg{execution.result, event.FileEditor.Reply}
	})
}

type fileEditorReturnedMsg struct {
	result FileEditorResult
	reply  chan FileEditorResult
}

func (m *terminalModel) finishTerminalFileEditor(msg fileEditorReturnedMsg) {
	m.state.Editing = false
	if msg.reply != nil {
		select {
		case msg.reply <- msg.result:
		case <-m.ctx.Done():
		}
	}
}
