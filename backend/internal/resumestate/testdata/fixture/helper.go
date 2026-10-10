package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/resumestate"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type helperInput struct{ Path, Mode string }
type helperFiles struct {
	path, mode string
	calls      *[]map[string]any
}

func (f helperFiles) Open(path string) (sessionstore.File, error) {
	if path != f.path {
		return nil, errors.New("helper requires exact original path")
	}
	value, err := (resumestate.OSFiles{}).Open(path)
	if err != nil {
		return nil, err
	}
	return &helperFile{File: value, mode: f.mode, calls: f.calls}, nil
}

type helperFile struct {
	sessionstore.File
	mode    string
	ordinal int
	calls   *[]map[string]any
}

func (f *helperFile) ReadAt(value []byte, position int64) (int, error) {
	f.ordinal++
	*f.calls = append(*f.calls, map[string]any{"operation": "read", "ordinal": f.ordinal, "position": position})
	if f.mode == "zero-head" && f.ordinal == 1 {
		return 0, nil
	}
	if f.mode == "tail-read" && f.ordinal == 2 {
		return 0, errors.New("original-private-tail-read-fault")
	}
	return f.File.ReadAt(value, position)
}
func (f *helperFile) Close() error {
	*f.calls = append(*f.calls, map[string]any{"operation": "close"})
	if err := f.File.Close(); err != nil {
		return err
	}
	if f.mode == "close" {
		return errors.New("original-private-close-fault")
	}
	return nil
}
func executeLiteHelper(input input) {
	value := input.LiteHelper
	must(guard(input.Home, value.Path, false))
	stat, exists := input.Stat[value.Path]
	if input.Source != nil || input.JSONLPath != "" || input.Request != nil || !exists || stat.Size <= 65536 || value.Mode != "tail-read" && value.Mode != "close" && value.Mode != "zero-head" {
		panic("direct lite helper requires exact private fault profile")
	}
	info, err := os.Stat(value.Path)
	must(err)
	if info.Size() != stat.Size {
		panic("original stat size differs")
	}
	calls := []map[string]any{}
	head, tail := resumestate.ReadHeadAndTail(helperFiles{value.Path, value.Mode, &calls}, value.Path, stat.Size)
	result := map[string]any{"head": head, "tail": tail, "calls": calls}
	raw, err := json.Marshal(result)
	must(err)
	must(os.WriteFile(filepath.Join(input.Home, "resume-state-result.json"), raw, 0600))
	fmt.Println(`{"result":"resume-state-result.json"}`)
}
