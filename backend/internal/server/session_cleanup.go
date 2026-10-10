package server

import (
	"bytes"
	"encoding/json"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

type sessionCleanupResult struct{ files, errors int }

// Source's explicit retention action operates on transcript, subagent and
// tool-result files only. DirEntry type checks do not follow symlinks.
func cleanupSessionFiles(projects string, cutoff time.Time, dryRun bool) sessionCleanupResult {
	result := sessionCleanupResult{}
	removeEmpty := func(path string) {
		if !dryRun {
			// Source rmdir must also reject a file replacing the directory.
			_ = syscall.Rmdir(path)
		}
	}
	unlinkOld := func(path string) {
		stat, err := os.Stat(path)
		if err == nil && stat.ModTime().Before(cutoff) {
			if !dryRun {
				err = os.Remove(path)
			}
			if err == nil {
				result.files++
			}
		}
		if err != nil {
			result.errors++
		}
	}
	var nested func(string)
	nested = func(path string) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, entry := range entries {
			child := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				nested(child)
				removeEmpty(child)
			} else if entry.Type().IsRegular() {
				unlinkOld(child)
			}
		}
	}
	toolResults := func(path string) {
		entries, err := os.ReadDir(path)
		if err != nil {
			return
		}
		for _, entry := range entries {
			child := filepath.Join(path, entry.Name())
			if entry.Type().IsRegular() {
				unlinkOld(child)
			} else if entry.IsDir() {
				nested(child)
				removeEmpty(child)
			}
		}
		removeEmpty(path)
	}
	projectsEntries, err := os.ReadDir(projects)
	if err != nil {
		return result
	}
	for _, project := range projectsEntries {
		if !project.IsDir() {
			continue
		}
		projectPath := filepath.Join(projects, project.Name())
		entries, err := os.ReadDir(projectPath)
		if err != nil {
			result.errors++
			continue
		}
		for _, entry := range entries {
			path := filepath.Join(projectPath, entry.Name())
			if entry.Type().IsRegular() {
				if strings.HasSuffix(entry.Name(), ".jsonl") || strings.HasSuffix(entry.Name(), ".cast") {
					unlinkOld(path)
				}
			} else if entry.IsDir() {
				nested(filepath.Join(path, "subagents"))
				toolResults(filepath.Join(path, "tool-results"))
				removeEmpty(path)
			}
		}
		removeEmpty(projectPath)
	}
	return result
}

func (server *Server) sessionCleanupHTTP(writer http.ResponseWriter, request *http.Request) error {
	if request.Method != http.MethodPost {
		return methodNotAllowed(request.Method)
	}
	body, err := jsonObject(request)
	if err != nil {
		return err
	}
	var days float64
	if bytes.Equal(bytes.TrimSpace(body["days"]), []byte("null")) || json.Unmarshal(body["days"], &days) != nil || math.IsNaN(days) || math.IsInf(days, 0) || math.Trunc(days) != days || days < 0 || days > 3650 {
		return badRequest(`"days" must be an integer between 0 and 3650`)
	}
	var dryRun bool
	_ = json.Unmarshal(body["dryRun"], &dryRun)
	cutoff := time.UnixMilli(time.Now().UnixMilli() - int64(days)*24*60*60*1000)
	result := cleanupSessionFiles(filepath.Join(server.options.ConfigDir, "projects"), cutoff, dryRun)
	if !dryRun && result.files > 0 {
		server.noteSessionMutation("")
	}
	writeJSON(writer, http.StatusOK, map[string]any{"ok": true, "days": days, "dryRun": dryRun, "files": result.files, "errors": result.errors})
	return nil
}
