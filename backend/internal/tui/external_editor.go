package tui

import (
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"strings"
)

// ExternalEditor retains the host's selected executable and arguments. Shell
// commands, GUI wait flags and editor discovery are resolved by that host.
type ExternalEditor struct {
	Command     []string
	Environment []string
	TempDir     string
}

type editorExecution struct {
	ctx     context.Context
	options ExternalEditor
	text    string
	changed bool
	stdin   io.Reader
	stdout  io.Writer
	stderr  io.Writer
}

func newEditorExecution(ctx context.Context, options ExternalEditor, text string, input io.Reader, output io.Writer) *editorExecution {
	options.Command = append([]string(nil), options.Command...)
	options.Environment = append([]string{}, options.Environment...)
	return &editorExecution{ctx: ctx, options: options, text: text, stdin: input, stdout: output, stderr: output}
}

func (e *editorExecution) SetStdin(input io.Reader) {
	if e.stdin == nil {
		e.stdin = input
	}
}
func (e *editorExecution) SetStdout(output io.Writer) {
	if e.stdout == nil {
		e.stdout = output
	}
}
func (e *editorExecution) SetStderr(output io.Writer) {
	if e.stderr == nil {
		e.stderr = output
	}
}

func (e *editorExecution) Run() (err error) {
	if len(e.options.Command) == 0 || e.options.Command[0] == "" || e.options.TempDir == "" {
		return errors.New("External editor requires an executable and explicit temporary directory")
	}
	file, err := os.CreateTemp(e.options.TempDir, "claude-prompt-*")
	if err != nil {
		return err
	}
	path := file.Name()
	defer func() { err = errors.Join(err, os.Remove(path)) }()
	if _, err = file.WriteString(e.text); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	args := append([]string(nil), e.options.Command[1:]...)
	// Preserve the original prompt editor's overrides for exact commands.
	if len(e.options.Command) == 1 {
		switch e.options.Command[0] {
		case "code":
			args = append(args, "-w")
		case "subl":
			args = append(args, "--wait")
		}
	}
	command := exec.CommandContext(e.ctx, e.options.Command[0], append(args, path)...)
	command.Env = e.options.Environment
	command.Stdin, command.Stdout, command.Stderr = e.stdin, e.stdout, e.stderr
	if err = command.Run(); err != nil {
		return err
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	text := string(content)
	if strings.HasSuffix(text, "\n") && !strings.HasSuffix(text, "\n\n") {
		text = strings.TrimSuffix(text, "\n")
	}
	e.changed = text != e.text
	e.text = text
	return nil
}
