package main

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionlocator"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type step struct {
	Operation     string  `json:"operation"`
	SessionID     *string `json:"sessionId"`
	Project       *string `json:"project"`
	Bytes         string  `json:"bytes"`
	KeepAllLeaves bool    `json:"keepAllLeaves"`
}
type input struct {
	Home, SessionID, Project string
	Files                    []struct{ Project, Name string }
	Steps                    []step
	DisablePrecompactSkip    bool
}

func fail() error {
	return errors.New("Session history fixture requires disposable HOME/config and ordinary private files")
}
func inside(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func assertHome(home string) error {
	if !filepath.IsAbs(home) || strings.ContainsRune(home, 0) || home != os.Getenv("HOME") || os.Getenv("USERPROFILE") != home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		return fail()
	}
	info, err := os.Lstat(home)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonical, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	if !filepath.IsAbs(root) {
		return fail()
	}
	canonicalRoot, err := filepath.EvalSymlinks(root)
	if err != nil || canonical == canonicalRoot || !inside(canonicalRoot, canonical) {
		return fail()
	}
	cwd, err := os.Getwd()
	if err != nil {
		return fail()
	}
	canonicalCwd, err := filepath.EvalSymlinks(cwd)
	if err != nil || canonicalCwd != canonical {
		return fail()
	}
	config := filepath.Join(home, ".claude")
	if info, err := os.Lstat(config); errors.Is(err, os.ErrNotExist) {
		return nil
	} else if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fail()
	}
	canonicalConfig, err := filepath.EvalSymlinks(config)
	if err != nil || !inside(canonical, canonicalConfig) {
		return fail()
	}
	return nil
}
func guard(home, path string, ordinary bool) error {
	if !inside(home, path) {
		return fail()
	}
	canonicalHome, err := filepath.EvalSymlinks(home)
	if err != nil {
		return fail()
	}
	for probe := path; ; probe = filepath.Dir(probe) {
		info, err := os.Lstat(probe)
		if errors.Is(err, os.ErrNotExist) {
			if probe == filepath.Dir(probe) {
				return fail()
			}
			continue
		}
		if err != nil || info.Mode()&os.ModeSymlink != 0 || probe == path && ordinary && !info.Mode().IsRegular() {
			return fail()
		}
		canonical, err := filepath.EvalSymlinks(probe)
		if err != nil || !inside(canonicalHome, canonical) {
			return fail()
		}
		return nil
	}
}

type privateFS struct{ home string }

func (files privateFS) Open(path string) (sessionstore.File, error) {
	if err := guard(files.home, path, true); err != nil {
		panic(err)
	}
	return (sessionstore.OSFileSystem{}).Open(path)
}
func must(err error) {
	if err != nil {
		panic(err)
	}
}
func main() {
	var options input
	must(json.NewDecoder(os.Stdin).Decode(&options))
	must(assertHome(options.Home))
	output := filepath.Join(options.Home, "history-result.json")
	for _, entry := range options.Files {
		must(guard(options.Home, filepath.Join(options.Home, ".claude", "projects", entry.Project, entry.Name), true))
	}
	must(guard(options.Home, output, true))
	files := privateFS{options.Home}
	events := []map[string]any{}
	rawErrors := []map[string]any{}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: files, Stat: func(path string) (int64, error) {
		must(guard(options.Home, path, false))
		info, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return info.Size(), nil
	}, ReadFile: func(path string) ([]byte, error) { must(guard(options.Home, path, false)); return os.ReadFile(path) }, DisablePrecompactSkip: func() bool { return options.DisablePrecompactSkip }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(event sessionhistory.Diagnostic) {
		if event.Message != "" {
			rawErrors = append(rawErrors, map[string]any{"name": "Error", "message": event.Message})
		}
		events = append(events, map[string]any{"eventName": event.Code, "metadata": event.Fields})
	}})
	must(err)
	budget := sessionstore.NewBudget()
	locator, err := sessionlocator.New(sessionlocator.Options{ConfigDir: func() string { return filepath.Join(options.Home, ".claude") }, Reader: sessionstore.NewReaderWithBudget(files, budget), Budget: budget, Compare: sessionhistory.CompareStrings, ReadDir: func(path string) ([]string, error) {
		must(guard(options.Home, path, false))
		values, err := os.ReadDir(path)
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, value := range values {
			names = append(names, value.Name())
		}
		return names, nil
	}, Stat: func(path string) (float64, error) {
		must(guard(options.Home, path, false))
		value, err := os.Stat(path)
		if err != nil {
			return 0, err
		}
		return float64(value.ModTime().Unix())*1000 + float64(value.ModTime().Nanosecond())/1e6, nil
	}})
	must(err)
	results := []map[string]any{}
	for index, current := range options.Steps {
		id, project := options.SessionID, options.Project
		if current.SessionID != nil {
			id = *current.SessionID
		}
		if current.Project != nil {
			project = *current.Project
		}
		path := filepath.Join(options.Home, ".claude", "projects", project, id+".jsonl")
		var value any
		var err error
		switch current.Operation {
		case "append", "rewrite":
			must(guard(options.Home, path, true))
			data, decodeErr := hex.DecodeString(current.Bytes)
			must(decodeErr)
			flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
			if current.Operation == "append" {
				flags = os.O_WRONLY | os.O_CREATE | os.O_APPEND
			}
			file, openErr := os.OpenFile(path, flags, 0600)
			err = openErr
			if err == nil {
				_, err = file.Write(data)
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err == nil {
				stamp := time.Unix(1700000100+int64(index), 0)
				err = os.Chtimes(path, stamp, stamp)
			}
			value = map[string]bool{"ok": true}
		case "transcript":
			value, err = loader.LoadTranscriptFile(path, current.KeepAllLeaves)
		case "path":
			value, err = loader.LoadMessagesFromJSONLPath(path)
		case "locate":
			value, err = locator.FindFile(id)
		case "resume":
			located, locateErr := locator.FindFile(id)
			err = locateErr
			fields := protocol.NewJSONObject()
			raw, _ := json.Marshal(located)
			fields.Set("located", raw)
			messages := sessionhistory.ResumeResult{Messages: []json.RawMessage{}}
			if err == nil && located != nil {
				messages, err = loader.LoadMessagesFromJSONLPath(located.FilePath)
			}
			raw, _ = json.Marshal(messages.Messages)
			fields.Set("messages", raw)
			fields.Set("sessionId", messages.SessionID)
			value = fields
		default:
			panic("unknown history fixture operation")
		}
		if err != nil {
			name := "Error"
			if failure, ok := err.(interface{ ErrorName() string }); ok {
				name = failure.ErrorName()
			}
			rawErrors = append(rawErrors, map[string]any{"name": name, "message": err.Error()})
			results = append(results, map[string]any{"error": map[string]any{"name": name, "code": "ERROR", "message": err.Error()}})
			continue
		}
		raw, err := json.Marshal(value)
		must(err)
		raw, err = protocol.ParseAndStringifyJavaScriptJSON(raw)
		must(err)
		results = append(results, map[string]any{"bytes": hex.EncodeToString(raw)})
	}
	must(guard(options.Home, output, true))
	data, err := json.Marshal(map[string]any{"results": results, "events": events, "rawErrors": rawErrors})
	must(err)
	must(os.WriteFile(output, data, 0600))
	fmt.Println(`{"result":"history-result.json"}`)
}
