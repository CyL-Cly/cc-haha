package main

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type step struct {
	Operation  string          `json:"operation"`
	Bytes      string          `json:"bytes"`
	ProjectDir *string         `json:"projectDir"`
	Memory     json.RawMessage `json:"memory"`
	Count      *int            `json:"count"`
}
type input struct {
	Home         string `json:"home"`
	ProjectDir   string `json:"projectDir"`
	BirthtimeISO string `json:"birthtimeISO"`
	Steps        []step `json:"steps"`
}
type event struct {
	Operation string `json:"operation"`
	Offset    *int64 `json:"offset,omitempty"`
	Length    *int   `json:"length,omitempty"`
	BytesRead *int   `json:"bytesRead,omitempty"`
}
type tracedFS struct {
	home  string
	trace *[]event
	mu    sync.Mutex
}
type tracedFile struct {
	sessionstore.File
	owner *tracedFS
}

func fixtureFailure() error {
	return errors.New("Session projection fixture requires disposable HOME/config and ordinary private files")
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
		canonicalHome, err := filepath.EvalSymlinks(home)
		if err != nil {
			return fixtureFailure()
		}
		for ancestor := filepath.Dir(path); ; ancestor = filepath.Dir(ancestor) {
			if _, err := os.Lstat(ancestor); err == nil {
				canonical, err := filepath.EvalSymlinks(ancestor)
				if err != nil || canonical != canonicalHome && !childPath(canonicalHome, canonical) {
					return fixtureFailure()
				}
				return nil
			} else if !errors.Is(err, os.ErrNotExist) || filepath.Dir(ancestor) == ancestor {
				return fixtureFailure()
			}
		}
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
	if err := assertFixtureFile(fs.home, path); err != nil {
		return nil, err
	}
	fs.record(event{Operation: "open"})
	file, err := (sessionstore.OSFileSystem{}).Open(path)
	if err != nil {
		return nil, err
	}
	return &tracedFile{file, fs}, nil
}
func (fs *tracedFS) record(value event) {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	*fs.trace = append(*fs.trace, value)
}
func (file *tracedFile) ReadAt(buffer []byte, offset int64) (int, error) {
	n, err := file.File.ReadAt(buffer, offset)
	length := len(buffer)
	file.owner.record(event{Operation: "read", Offset: &offset, Length: &length, BytesRead: &n})
	return n, err
}
func (file *tracedFile) Stat() (sessionstore.Snapshot, error) {
	file.owner.record(event{Operation: "stat"})
	return file.File.Stat()
}
func (file *tracedFile) Close() error {
	file.owner.record(event{Operation: "close"})
	return file.File.Close()
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
	const id = "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	file := filepath.Join(options.Home, ".claude", "projects", options.ProjectDir, id+".jsonl")
	output := filepath.Join(options.Home, "projection-result.json")
	for _, path := range []string{file, output} {
		if err := assertFixtureFile(options.Home, path); err != nil {
			panic(err)
		}
	}
	trace := []event{}
	files := &tracedFS{home: options.Home, trace: &trace}
	budget := sessionstore.NewBudget()
	makeService := func() *sessionprojection.Service {
		service, err := sessionprojection.New(sessionprojection.Options{Reader: sessionmetadata.NewReader(files), Budget: budget, ConfigDir: func() string { return filepath.Join(options.Home, ".claude") }, Cwd: func() string {
			cwd, err := os.Getwd()
			if err != nil {
				panic(err)
			}
			canonical, err := filepath.EvalSymlinks(cwd)
			if err != nil {
				panic(err)
			}
			return canonical
		}, Platform: map[string]string{"darwin": "darwin", "linux": "linux", "windows": "win32"}[runtime.GOOS], ParseDate: sessionprojection.ParseDateUTC, Stat: func(path string) (sessionprojection.Stat, error) {
			files.record(event{Operation: "pathStat"})
			if err := assertFixtureFile(options.Home, path); err != nil {
				return sessionprojection.Stat{}, err
			}
			handle, err := (sessionstore.OSFileSystem{}).Open(path)
			if err != nil {
				return sessionprojection.Stat{}, err
			}
			info, err := handle.Stat()
			closeErr := handle.Close()
			if err != nil {
				return sessionprojection.Stat{}, err
			}
			if closeErr != nil {
				return sessionprojection.Stat{}, closeErr
			}
			native, err := os.Stat(path)
			if err != nil {
				return sessionprojection.Stat{}, err
			}
			return sessionprojection.Stat{SourceVersion: fmt.Sprintf("%s:%s:%d:%s", info.Dev, info.Ino, info.Size, info.MtimeNS), BirthtimeISO: options.BirthtimeISO, MtimeISO: native.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")}, nil
		}})
		if err != nil {
			panic(err)
		}
		return service
	}
	service := makeService()
	results := []map[string]any{}
	var memory json.RawMessage
	for index, current := range options.Steps {
		var value any
		var err error
		project := options.ProjectDir
		if current.ProjectDir != nil {
			project = *current.ProjectDir
		}
		switch current.Operation {
		case "append", "rewrite":
			if err := assertFixtureFile(options.Home, file); err != nil {
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
			handle, openErr := os.OpenFile(file, flags, 0600)
			err = openErr
			if err == nil {
				_, err = handle.Write(data)
				_ = handle.Close()
			}
			if err == nil {
				stamp := time.Unix(1700000010+int64(index), 0)
				err = os.Chtimes(file, stamp, stamp)
			}
			value = map[string]bool{"ok": true}
		case "restart":
			service = makeService()
			memory = nil
			value = map[string]bool{"ok": true}
		case "launch":
			if current.Memory != nil {
				memory = current.Memory
			}
			value, err = service.PersistedLaunchInfo(file, project, memory)
		case "projection":
			value, err = service.Get(file, project)
		case "concurrent":
			count := 4
			if current.Count != nil {
				count = *current.Count
			}
			if count < 0 {
				count = 0
			}
			values := make([]sessionprojection.Result, count)
			failures := make([]error, count)
			var group sync.WaitGroup
			for index := range values {
				group.Add(1)
				go func(index int) {
					defer group.Done()
					values[index], failures[index] = service.Get(file, project)
				}(index)
			}
			group.Wait()
			for _, failure := range failures {
				if failure != nil {
					err = failure
					break
				}
			}
			value = values
		default:
			panic("unsupported projection operation")
		}
		if err != nil {
			failure := map[string]any{"name": "Error", "code": "ERROR", "message": err.Error()}
			var api *sessionstore.APIError
			var typeError *sessionprojection.TypeError
			if errors.As(err, &api) {
				failure["name"] = "ApiError"
				failure["code"] = api.Code
				failure["status"] = api.StatusCode
			}
			if errors.As(err, &typeError) {
				failure["name"] = "TypeError"
			}
			results = append(results, map[string]any{"error": failure})
		} else {
			results = append(results, map[string]any{"bytes": hex.EncodeToString(encode(value))})
		}
	}
	if err := assertFixtureFile(options.Home, output); err != nil {
		panic(err)
	}
	if err := os.WriteFile(output, encode(struct {
		Results any `json:"results"`
		Trace   any `json:"trace"`
	}{results, trace}), 0600); err != nil {
		panic(err)
	}
	_, _ = io.WriteString(os.Stdout, fmt.Sprintln(`{"result":"projection-result.json"}`))
}
