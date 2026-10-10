package clisettings

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
	"strings"
)

type LocalIgnoreOptions struct {
	Home       string
	Stat       func(string) (os.FileInfo, error)
	RunGit     func(context.Context, string, []string) (int, error)
	MkdirAll   func(string) error
	ReadFile   func(string) ([]byte, error)
	AppendFile func(string, []byte) error
	WriteFile  func(string, []byte) error
	LogError   func(error)
}
type LocalIgnore struct{ options LocalIgnoreOptions }

func NewLocalIgnore(o LocalIgnoreOptions) (*LocalIgnore, error) {
	if !filepath.IsAbs(o.Home) || o.Stat == nil || o.RunGit == nil || o.MkdirAll == nil || o.ReadFile == nil || o.AppendFile == nil || o.WriteFile == nil || o.LogError == nil {
		return nil, errors.New("local ignore requires owned HOME/files/git/error producers")
	}
	return &LocalIgnore{o}, nil
}
func (s *LocalIgnore) Run(ctx context.Context, filename, cwd string) error {
	if e := s.run(ctx, filename, cwd); e != nil {
		s.options.LogError(e)
	}
	// The original asynchronous helper logs its own failures and resolves.
	return nil
}
func (s *LocalIgnore) run(ctx context.Context, filename, cwd string) error {
	inRepo := false
	for current := filepath.Clean(cwd); ; current = filepath.Dir(current) {
		if info, e := s.options.Stat(filepath.Join(current, ".git")); e == nil && (info.IsDir() || info.Mode().IsRegular()) {
			inRepo = true
			break
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	if !inRepo {
		return nil
	}
	entry := "**/" + filename
	testPath := filename
	if strings.HasSuffix(filename, "/") {
		testPath += "sample-file.txt"
	}
	code, e := s.options.RunGit(ctx, cwd, []string{"check-ignore", testPath})
	if e != nil {
		// Source execFileNoThrow logs process failures and resolves code 1;
		// the global-ignore write still proceeds after that failed check.
		s.options.LogError(e)
		code = 1
	}
	if code == 0 {
		return nil
	}
	path := filepath.Join(s.options.Home, ".config", "git", "ignore")
	if e = s.options.MkdirAll(filepath.Dir(path)); e != nil {
		return e
	}
	data, e := s.options.ReadFile(path)
	if e == nil {
		if strings.Contains(string(protocol.DecodeJavaScriptUTF8(data)), entry) {
			return nil
		}
		return s.options.AppendFile(path, []byte("\n"+entry+"\n"))
	}
	if os.IsNotExist(e) {
		return s.options.WriteFile(path, []byte(entry+"\n"))
	}
	return e
}
