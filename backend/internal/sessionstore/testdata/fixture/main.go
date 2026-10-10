package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type step struct {
	Operation      string          `json:"operation"`
	CursorFrom     *int            `json:"cursorFrom"`
	Previous       bool            `json:"previous"`
	Cursor         string          `json:"cursor"`
	Limit          *float64        `json:"limit"`
	Full           bool            `json:"full"`
	StartOffset    int64           `json:"startOffset"`
	EndOffset      *int64          `json:"endOffset"`
	MaxRecordBytes *int64          `json:"maxRecordBytes"`
	FailConsumer   bool            `json:"failConsumer"`
	Bytes          string          `json:"bytes"`
	Raw            json.RawMessage `json:"raw"`
	ModifyCursor   json.RawMessage `json:"modifyCursor"`
	Legacy         bool            `json:"legacy"`
}
type input struct {
	Home           string    `json:"home"`
	Steps          []step    `json:"steps"`
	MaxRead        int       `json:"maxRead"`
	MutateOnRead   *mutation `json:"mutateOnRead"`
	HashResults    bool      `json:"hashResults"`
	FailCloseCount int       `json:"failCloseCount"`
}
type mutation struct {
	Index        int    `json:"index"`
	Operation    string `json:"operation"`
	Bytes        string `json:"bytes"`
	PreserveTime bool   `json:"preserveTime"`
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
}
type tracedFile struct {
	sessionstore.File
	owner *tracedFS
}

func fixtureFailure() error {
	return errors.New("Session reader fixture requires disposable HOME/config and ordinary private files")
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
		return nil, errors.New("Session reader escaped HOME")
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
		path := filepath.Join(file.owner.home, "session.jsonl")
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
func modify(value string, updates json.RawMessage, legacy bool) string {
	raw, _ := base64.RawURLEncoding.DecodeString(value)
	var object protocol.JSONObject
	if object.UnmarshalJSON(raw) != nil {
		return value
	}
	if legacy {
		object.Delete("fingerprints")
	}
	if len(updates) > 0 && updates[0] == '{' {
		decoder := json.NewDecoder(bytes.NewReader(updates))
		_, _ = decoder.Token()
		for decoder.More() {
			name, _ := decoder.Token()
			var field json.RawMessage
			_ = decoder.Decode(&field)
			object.Set(name.(string), field)
		}
	}
	raw, err := object.MarshalJSON()
	if err != nil {
		panic(err)
	}
	raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
	if err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

// Waiting for the actual cancellation subscription establishes enqueue order
// without scheduler sleeps or a test-only production admission accessor.
type queuedContext struct {
	context.Context
	queued chan struct{}
	once   sync.Once
}

func (ctx *queuedContext) Done() <-chan struct{} {
	ctx.once.Do(func() { close(ctx.queued) })
	return ctx.Context.Done()
}
func admissionProfile(budget *sessionstore.Budget, lane sessionstore.Lane) any {
	limits := map[sessionstore.Lane][2]int{sessionstore.PageLane: {2, 8}, sessionstore.ContextLane: {1, 4}, sessionstore.RecoveryLane: {1, 2}, sessionstore.MetadataLane: {2, 128}}
	limit := limits[lane]
	total := limit[0] + limit[1] + 1
	holds := make([]chan struct{}, total+2)
	entered := make([]chan struct{}, total+2)
	finished := make([]chan struct{}, total+2)
	for index := range holds {
		holds[index] = make(chan struct{})
		entered[index] = make(chan struct{})
		finished[index] = make(chan struct{})
	}
	started := []int{}
	type failure struct {
		Name    string `json:"name"`
		Message string `json:"message"`
		Code    string `json:"code,omitempty"`
		Status  int    `json:"status,omitempty"`
	}
	failures := []failure{}
	var mutex sync.Mutex
	var group sync.WaitGroup
	cancel := make([]context.CancelCauseFunc, total)
	begin := func(index int, ctx context.Context) {
		observed := &queuedContext{Context: ctx, queued: make(chan struct{})}
		group.Add(1)
		go func() {
			defer group.Done()
			defer close(finished[index])
			err := budget.With(observed, lane, func() error {
				mutex.Lock()
				started = append(started, index)
				mutex.Unlock()
				close(entered[index])
				<-holds[index]
				return nil
			})
			if err != nil {
				result := failure{Name: "AbortError", Message: err.Error()}
				var api *sessionstore.APIError
				if errors.As(err, &api) {
					result = failure{Name: "ApiError", Message: api.Message, Code: api.Code, Status: api.StatusCode}
				}
				mutex.Lock()
				failures = append(failures, result)
				mutex.Unlock()
			}
		}()
		select {
		case <-observed.queued:
		case <-entered[index]:
		case <-finished[index]:
		}
	}
	for index := 0; index < total; index++ {
		ctx, canceller := context.WithCancelCause(context.Background())
		cancel[index] = canceller
		begin(index, ctx)
	}
	cancel[limit[0]](errors.New("tab closed"))
	<-finished[limit[0]]
	begin(total, context.Background())
	last := 0
	for index := limit[0] + 1; index < total-1; index++ {
		close(holds[last])
		<-entered[index]
		last = index
	}
	close(holds[last])
	<-entered[total]
	close(holds[total])
	for index := 1; index < limit[0]; index++ {
		close(holds[index])
	}
	group.Wait()
	close(holds[total+1])
	begin(total+1, context.Background())
	group.Wait()
	return struct {
		Started  []int `json:"started"`
		Failures any   `json:"failures"`
	}{started, failures}
}
func main() {
	var options input
	if err := json.NewDecoder(os.Stdin).Decode(&options); err != nil {
		panic(err)
	}
	if err := assertFixtureHome(options.Home); err != nil {
		panic(err)
	}
	path := filepath.Join(options.Home, "session.jsonl")
	resultPath := filepath.Join(options.Home, "reader-result.json")
	for _, candidate := range []string{path, resultPath} {
		if err := assertFixtureFile(options.Home, candidate); err != nil {
			panic(err)
		}
	}
	trace := []event{}
	reader := sessionstore.NewReader(&tracedFS{trace: &trace, maxRead: options.MaxRead, home: options.Home, mutation: options.MutateOnRead, closeFailures: options.FailCloseCount})
	pages := map[int]sessionstore.Page{}
	results := []map[string]any{}
	for index, current := range options.Steps {
		var value any
		var err error
		switch current.Operation {
		case "append", "rewrite":
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
		case "page":
			cursor := current.Cursor
			if current.CursorFrom != nil {
				page := pages[*current.CursorFrom]
				selected := page.Page.NextCursor
				if current.Previous {
					selected = page.Page.PreviousCursor
				}
				if selected != nil {
					cursor = *selected
				}
			}
			if cursor != "" && (len(current.ModifyCursor) > 0 || current.Legacy) {
				cursor = modify(cursor, current.ModifyCursor, current.Legacy)
			}
			var page sessionstore.Page
			page, err = reader.ReadPage(context.Background(), path, sessionstore.PageOptions{Cursor: cursor, Limit: current.Limit, Full: current.Full})
			if err == nil {
				pages[index] = page
			}
			value = page
		case "stream":
			entries := []struct {
				Entry    json.RawMessage `json:"entry"`
				Complete bool            `json:"complete"`
				Start    int64           `json:"start"`
			}{}
			skipped := 0
			var scan sessionstore.StreamResult
			scan, err = reader.Stream(context.Background(), path, func(raw json.RawMessage, complete bool, start int64) error {
				if current.FailConsumer {
					return errors.New("fixture consumer failed")
				}
				entries = append(entries, struct {
					Entry    json.RawMessage `json:"entry"`
					Complete bool            `json:"complete"`
					Start    int64           `json:"start"`
				}{raw, complete, start})
				return nil
			}, sessionstore.StreamOptions{StartOffset: current.StartOffset, EndOffset: current.EndOffset, MaxRecordBytes: current.MaxRecordBytes, OnSkipped: func() { skipped++ }})
			value = struct {
				Entries any                       `json:"entries"`
				Scan    sessionstore.StreamResult `json:"scan"`
				Skipped int                       `json:"skipped"`
			}{entries, scan, skipped}
		case "preview":
			raw := current.Raw
			var quoted string
			if json.Unmarshal(raw, &quoted) == nil {
				raw = []byte(quoted)
			}
			if len(raw) == 0 {
				raw = []byte("{}")
			}
			value, err = sessionstore.DisplayPreview(raw)
		case "admission":
			var lane string
			_ = json.Unmarshal(current.Raw, &lane)
			value = admissionProfile(reader.Budget(), sessionstore.Lane(lane))
		case "cancelled":
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			_, failure := reader.ReadPage(ctx, path, sessionstore.PageOptions{})
			var abort *sessionstore.AbortError
			if !errors.As(failure, &abort) {
				panic("cancelled reader did not preserve AbortError")
			}
			value = struct {
				Name    string `json:"name"`
				Message string `json:"message"`
				Code    int    `json:"code"`
			}{abort.ErrorName(), abort.Error(), 20}
		default:
			panic("Invalid session reader operation")
		}
		if err != nil {
			var api *sessionstore.APIError
			if errors.As(err, &api) {
				results = append(results, map[string]any{"error": map[string]any{"code": api.Code, "status": api.StatusCode, "message": api.Message}})
			} else {
				results = append(results, map[string]any{"error": map[string]any{"code": "ERROR", "message": err.Error()}})
			}
		} else {
			serialized := encode(value)
			output := hex.EncodeToString(serialized)
			if options.HashResults {
				hash := sha256.Sum256(serialized)
				output = string(encode(struct {
					Bytes  int    `json:"bytes"`
					SHA256 string `json:"sha256"`
				}{len(serialized), hex.EncodeToString(hash[:])}))
			}
			results = append(results, map[string]any{"bytes": output})
		}
	}
	output := encode(struct {
		Results any `json:"results"`
		Trace   any `json:"trace"`
	}{results, trace})
	if err := assertFixtureFile(options.Home, resultPath); err != nil {
		panic(err)
	}
	if err := os.WriteFile(resultPath, output, 0600); err != nil {
		panic(err)
	}
	_, _ = io.WriteString(os.Stdout, fmt.Sprintf("%s\n", `{"result":"reader-result.json"}`))
}
