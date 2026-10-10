package main

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type step struct {
	Operation       string `json:"operation"`
	Bytes           string `json:"bytes"`
	StartOffset     int64  `json:"startOffset"`
	EndOffset       *int64 `json:"endOffset"`
	FailConsumer    bool   `json:"failConsumer"`
	FailSkipped     bool   `json:"failSkipped"`
	Aborted         bool   `json:"aborted"`
	CancelAfterRead int    `json:"cancelAfterRead"`
}
type mutation struct {
	Index        int    `json:"index"`
	Operation    string `json:"operation"`
	Bytes        string `json:"bytes"`
	PreserveTime bool   `json:"preserveTime"`
}
type input struct {
	Home           string    `json:"home"`
	Steps          []step    `json:"steps"`
	MaxRead        int       `json:"maxRead"`
	FailCloseCount int       `json:"failCloseCount"`
	MutateOnRead   *mutation `json:"mutateOnRead"`
}
type event struct {
	Operation string `json:"operation"`
	Offset    *int64 `json:"offset,omitempty"`
	Length    *int   `json:"length,omitempty"`
	BytesRead *int   `json:"bytesRead,omitempty"`
}
type tracedFS struct {
	files         sessionstore.OSFileSystem
	trace         *[]event
	maxRead       int
	home          string
	mutation      *mutation
	reads         int
	closeFailures int
	cancel        context.CancelFunc
	cancelAt      int
}
type tracedFile struct {
	sessionstore.File
	owner *tracedFS
}

func fixtureFailure() error {
	return errors.New("Session metadata fixture requires disposable HOME/config and ordinary private files")
}

func childPath(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func assertFixtureHome(home string) error {
	if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) || home != os.Getenv("HOME") || os.Getenv("USERPROFILE") != home || os.Getenv("BUN_OPTIONS") != "--no-env-file" || filepath.Join(home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") {
		return fixtureFailure()
	}
	info, err := os.Lstat(home)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.IsDir() {
		return fixtureFailure()
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fixtureFailure()
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(root) {
		return fixtureFailure()
	}
	rootInfo, err := os.Stat(root)
	if err != nil || !rootInfo.IsDir() {
		return fixtureFailure()
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || !childPath(canonicalRoot, canonical) {
		return fixtureFailure()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fixtureFailure()
	}
	canonicalCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil || canonicalCwd != canonical {
		return fixtureFailure()
	}
	config := filepath.Join(home, ".claude")
	configInfo, err := os.Lstat(config)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || configInfo.Mode()&os.ModeSymlink != 0 || !configInfo.IsDir() {
		return fixtureFailure()
	}
	canonicalConfig, err := filepath.EvalSymlinks(config)
	if err != nil || !childPath(canonical, canonicalConfig) {
		return fixtureFailure()
	}
	return nil
}

func assertFixtureFile(home, path string) error {
	if !childPath(home, path) {
		return fixtureFailure()
	}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return fixtureFailure()
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fixtureFailure()
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil || !childPath(canonicalHome, canonical) {
		return fixtureFailure()
	}
	return nil
}

func (fs *tracedFS) Open(path string) (sessionstore.File, error) {
	relative, err := filepath.Rel(fs.home, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return nil, errors.New("Session metadata escaped HOME")
	}
	if err := assertFixtureFile(fs.home, path); err != nil {
		return nil, err
	}
	*fs.trace = append(*fs.trace, event{Operation: "open"})
	file, err := fs.files.Open(path)
	if err != nil {
		return nil, err
	}
	return &tracedFile{file, fs}, nil
}
func (file *tracedFile) ReadAt(buffer []byte, offset int64) (int, error) {
	file.owner.reads++
	if change := file.owner.mutation; change != nil && change.Index == file.owner.reads {
		data, err := hex.DecodeString(change.Bytes)
		if err != nil {
			return 0, err
		}
		path := filepath.Join(file.owner.home, "metadata.jsonl")
		if err := assertFixtureFile(file.owner.home, path); err != nil {
			return 0, err
		}
		flags := os.O_WRONLY | os.O_TRUNC
		if change.Operation == "append" {
			flags = os.O_WRONLY | os.O_APPEND
		}
		writer, err := os.OpenFile(path, flags, 0600)
		if err != nil {
			return 0, err
		}
		_, err = writer.Write(data)
		_ = writer.Close()
		if err != nil {
			return 0, err
		}
		seconds := int64(1700000020)
		if change.PreserveTime {
			seconds = 1700000000
		}
		stamp := time.Unix(seconds, 0)
		if err = os.Chtimes(path, stamp, stamp); err != nil {
			return 0, err
		}
	}
	length := len(buffer)
	if file.owner.maxRead > 0 && len(buffer) > file.owner.maxRead {
		buffer = buffer[:file.owner.maxRead]
	}
	n, err := file.File.ReadAt(buffer, offset)
	*file.owner.trace = append(*file.owner.trace, event{Operation: "read", Offset: &offset, Length: &length, BytesRead: &n})
	if file.owner.reads == file.owner.cancelAt && file.owner.cancel != nil {
		file.owner.cancel()
	}
	return n, err
}
func (file *tracedFile) Stat() (sessionstore.Snapshot, error) {
	*file.owner.trace = append(*file.owner.trace, event{Operation: "stat"})
	return file.File.Stat()
}
func (file *tracedFile) Close() error {
	*file.owner.trace = append(*file.owner.trace, event{Operation: "close"})
	if err := file.File.Close(); err != nil {
		return err
	}
	if file.owner.closeFailures > 0 {
		file.owner.closeFailures--
		return errors.New("fixture close failed")
	}
	return nil
}
func encode(value any) []byte {
	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(value); err != nil {
		panic(err)
	}
	raw, err := protocol.ParseAndStringifyJavaScriptJSON(bytes.TrimSpace(output.Bytes()))
	if err != nil {
		panic(err)
	}
	return raw
}
func main() {
	var options input
	if err := json.NewDecoder(os.Stdin).Decode(&options); err != nil {
		panic(err)
	}
	if err := assertFixtureHome(options.Home); err != nil {
		panic(err)
	}
	path := filepath.Join(options.Home, "metadata.jsonl")
	resultPath := filepath.Join(options.Home, "metadata-result.json")
	for _, candidate := range []string{path, resultPath} {
		if err := assertFixtureFile(options.Home, candidate); err != nil {
			panic(err)
		}
	}
	trace := []event{}
	files := &tracedFS{trace: &trace, maxRead: options.MaxRead, home: options.Home, mutation: options.MutateOnRead, closeFailures: options.FailCloseCount}
	reader := sessionmetadata.NewReader(files)
	results := []map[string]any{}
	for index, current := range options.Steps {
		var value any
		var err error
		if current.Operation == "append" || current.Operation == "rewrite" {
			if err := assertFixtureFile(options.Home, path); err != nil {
				panic(err)
			}
			data, decodeErr := hex.DecodeString(current.Bytes)
			if decodeErr != nil {
				panic(decodeErr)
			}
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if current.Operation == "append" {
				flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
			}
			file, openErr := os.OpenFile(path, flags, 0600)
			err = openErr
			if err == nil {
				_, err = file.Write(data)
				_ = file.Close()
			}
			if err == nil {
				stamp := time.Unix(1700000010+int64(index), 0)
				err = os.Chtimes(path, stamp, stamp)
			}
			value = map[string]bool{"ok": true}
		} else if current.Operation == "stream" {
			ctx, cancel := context.WithCancel(context.Background())
			files.cancel = cancel
			files.cancelAt = current.CancelAfterRead
			if current.Aborted {
				cancel()
			}
			entries := []struct {
				Entry     json.RawMessage `json:"entry"`
				Complete  bool            `json:"complete"`
				Start     int64           `json:"start"`
				Truncated bool            `json:"truncated"`
			}{}
			skipped := 0
			var scan sessionmetadata.Result
			scan, err = reader.Stream(ctx, path, func(entry sessionmetadata.Entry, complete bool, start int64) error {
				if current.FailConsumer {
					return errors.New("fixture consumer failed")
				}
				entries = append(entries, struct {
					Entry     json.RawMessage `json:"entry"`
					Complete  bool            `json:"complete"`
					Start     int64           `json:"start"`
					Truncated bool            `json:"truncated"`
				}{entry.Value, complete, start, entry.TextTruncated})
				return nil
			}, sessionmetadata.Options{StartOffset: current.StartOffset, EndOffset: current.EndOffset, OnSkipped: func() error {
				if current.FailSkipped {
					return errors.New("fixture skipped failed")
				}
				skipped++
				return nil
			}})
			cancel()
			value = struct {
				Entries any                    `json:"entries"`
				Scan    sessionmetadata.Result `json:"scan"`
				Skipped int                    `json:"skipped"`
			}{entries, scan, skipped}
		} else {
			panic("Invalid metadata operation")
		}
		if err != nil {
			failure := map[string]any{"name": "Error", "code": "ERROR", "message": err.Error()}
			var api *sessionstore.APIError
			var abort *sessionstore.AbortError
			if errors.As(err, &api) {
				failure["name"] = "ApiError"
				failure["code"] = api.Code
				failure["status"] = api.StatusCode
			}
			if errors.As(err, &abort) {
				failure["name"] = "AbortError"
				failure["code"] = 20
			}
			results = append(results, map[string]any{"error": failure})
		} else {
			results = append(results, map[string]any{"bytes": hex.EncodeToString(encode(value))})
		}
	}
	if err := assertFixtureFile(options.Home, resultPath); err != nil {
		panic(err)
	}
	if err := os.WriteFile(resultPath, encode(struct {
		Results any `json:"results"`
		Trace   any `json:"trace"`
	}{results, trace}), 0600); err != nil {
		panic(err)
	}
	_, _ = io.WriteString(os.Stdout, fmt.Sprintln(`{"result":"metadata-result.json"}`))
}
