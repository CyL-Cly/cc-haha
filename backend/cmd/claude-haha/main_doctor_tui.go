package main

import (
	"context"
	"io"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/clibootstrap"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"github.com/charmbracelet/x/term"
)

func runNativeDoctorCommand(ctx context.Context, m *clibootstrap.MainCommand, out io.Writer) error {
	load := func(worker context.Context) (text string, err error) {
		err = m.Run(worker, func(scope *cooperative.Scope) error { text, err = m.Doctor(worker, scope); return err })
		return
	}
	file, outputIsFile := out.(term.File)
	if term.IsTerminal(os.Stdin.Fd()) && outputIsFile && term.IsTerminal(file.Fd()) {
		return tui.RunDoctorDialog(ctx, load, tui.Options{Input: os.Stdin, Output: out, Environment: os.Environ()})
	}
	text, err := load(ctx)
	if err == nil {
		_, err = io.WriteString(out, text)
	}
	return err
}
