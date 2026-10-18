package application

import (
	"context"
	"errors"
	"sort"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func (s *Session) editTerminalFile(ctx context.Context, path, editor, directory string, environment map[string]string) (string, error) {
	s.mu.Lock()
	emit := s.emit
	s.mu.Unlock()
	if emit == nil {
		return "", errors.New("terminal editor rendering consumer unavailable")
	}
	values := make([]string, 0, len(environment))
	for name, value := range environment {
		values = append(values, name+"="+value)
	}
	sort.Strings(values)
	reply := make(chan tui.FileEditorResult, 1)
	request := &tui.FileEditorRequest{Context: ctx, Directory: directory, Path: path, Editor: editor, Environment: values, Reply: reply}
	if err := emit(tui.Event{Kind: tui.FileEditorRequested, FileEditor: request}); err != nil {
		return "", err
	}
	select {
	case result := <-reply:
		return result.Error, result.Failure
	case <-ctx.Done():
		return "", context.Cause(ctx)
	}
}
