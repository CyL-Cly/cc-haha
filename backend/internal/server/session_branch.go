package server

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func (s *Server) branchSessionHTTP(w http.ResponseWriter, request *http.Request, id string) error {
	if request.Method != http.MethodPost {
		return methodNotAllowed(request.Method)
	}
	raw, err := io.ReadAll(request.Body)
	if err != nil || !json.Valid(raw) {
		return badRequest("Invalid JSON body")
	}
	var body protocol.JSONObject
	if err = json.Unmarshal(raw, &body); err != nil {
		return badRequest("targetMessageId (string) is required in request body")
	}
	targetRaw := body.Get("targetMessageId")
	var target string
	if len(targetRaw) == 0 || targetRaw[0] != '"' || json.Unmarshal(targetRaw, &target) != nil || protocol.TrimJavaScriptString(target) == "" {
		return badRequest("targetMessageId (string) is required in request body")
	}
	target = protocol.TrimJavaScriptString(target)
	var title json.RawMessage
	if value := body.Get("title"); len(value) > 0 {
		if value[0] != '"' {
			return badRequest("title must be a string")
		}
		units, e := protocol.DecodeJavaScriptJSONString(value)
		if e != nil {
			return e
		}
		start, stop := 0, len(units)
		for start < stop && protocol.IsJavaScriptWhitespace(rune(units[start])) {
			start++
		}
		for stop > start && protocol.IsJavaScriptWhitespace(rune(units[stop-1])) {
			stop--
		}
		if stop > start {
			title = protocol.QuoteJavaScriptUTF16(units[start:stop])
		}
	}
	info, err := s.findSession(request.Context(), id)
	if err != nil {
		return err
	}
	loader, err := sessionhistory.New(sessionhistory.Options{Files: sessionstore.OSFileSystem{}, Stat: func(path string) (int64, error) {
		v, e := os.Stat(path)
		if e != nil {
			return 0, e
		}
		return v.Size(), nil
	}, ReadFile: os.ReadFile, DisablePrecompactSkip: func() bool { return false }, LeafPrune: func() bool { return false }, ParseDate: sessionprojection.ParseDateUTC, Compare: sessionhistory.CompareStrings, OnDiagnostic: func(sessionhistory.Diagnostic) {}})
	if err != nil {
		return err
	}
	var meta protocol.JSONObject
	_ = json.Unmarshal(info.Metadata, &meta)
	workDir, _ := json.Marshal(info.WorkDir)
	if info.workDirNull {
		workDir = json.RawMessage("null")
	}
	result, err := loader.Branch(sessionhistory.BranchOptions{SourceSessionID: id, SourceTranscriptPath: info.Path, TargetMessageID: target, Title: title, SourceWorkDir: workDir, SourceRepository: meta.Get("repository"), SourceWorktreeSession: meta.Get("worktreeSession"), UUID: uuid, Now: time.Now})
	if err != nil {
		var failure *sessionhistory.BranchError
		if errors.As(err, &failure) {
			if failure.Code == "SOURCE_NOT_FOUND" {
				return notFound(failure.Message)
			}
			return badRequest(failure.Message)
		}
		return err
	}
	s.rememberKnown(result.SessionID)
	s.noteSessionMutation(result.ForkPath)
	s.invalidateRecentProjects()
	if string(result.WorkDir) == "null" {
		result.WorkDir = workDir
	}
	writeJSON(w, 201, map[string]any{"sessionId": result.SessionID, "title": result.Title, "workDir": result.WorkDir, "sourceSessionId": id, "targetMessageId": target})
	return nil
}
