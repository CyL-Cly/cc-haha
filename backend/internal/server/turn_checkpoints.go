package server

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionmetadata"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionrewind"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

func (server *Server) checkpointMessages(ctx context.Context, id string) (sessionrewind.Evidence, error) {
	info, err := server.findSession(ctx, id)
	if err != nil {
		return sessionrewind.Evidence{}, err
	}
	if info.ephemeral {
		return sessionrewind.Evidence{Messages: []protocol.JSONObject{}, Complete: false}, nil
	}
	entries, exists, complete, err := server.rawEvidenceTranscript(ctx, info.Path)
	if err != nil {
		return sessionrewind.Evidence{}, err
	}
	projected, _ := projectHistory(entries)
	projected, childrenComplete, err := server.appendCheckpointChildren(ctx, info, projected)
	if err != nil {
		return sessionrewind.Evidence{}, err
	}
	messages := make([]protocol.JSONObject, 0, len(projected))
	for _, raw := range projected {
		var message protocol.JSONObject
		if err := message.UnmarshalJSON(raw); err != nil {
			return sessionrewind.Evidence{}, err
		}
		messages = append(messages, message)
	}
	return sessionrewind.Evidence{Messages: messages, Complete: exists && complete && childrenComplete}, nil
}
func (server *Server) checkpointSnapshots(ctx context.Context, id string) (sessionrewind.SnapshotEvidence, error) {
	info, err := server.findSession(ctx, id)
	if err != nil {
		return sessionrewind.SnapshotEvidence{}, err
	}
	evidence := sessionrewind.SnapshotEvidence{Rows: []json.RawMessage{}, Complete: true}
	if info.memory {
		return evidence, nil
	}
	bytes := 0
	err = server.budget.With(ctx, sessionstore.MetadataLane, func() error {
		result, err := server.metadata.Stream(ctx, info.Path, func(entry sessionmetadata.Entry, _ bool, _ int64) error {
			record := historyRecord(entry.Value)
			if stringField(record, "type") != "file-history-snapshot" {
				return nil
			}
			raw, err := protocol.ParseAndStringifyJavaScriptJSON(entry.Value)
			if err != nil {
				return err
			}
			bytes += len(raw)
			if bytes > 2*1024*1024 || len(evidence.Rows) >= 1000 {
				return &apiError{413, "HISTORY_WORKSPACE_LIMIT", "File history exceeds its viewing budget"}
			}
			evidence.Rows = append(evidence.Rows, append(json.RawMessage(nil), entry.Value...))
			if entry.TextTruncated {
				evidence.Complete = false
			}
			return nil
		}, sessionmetadata.Options{})
		// The shared metadata parser can project records above its semantic limit;
		// this route never upgrades a lossy parse into complete backup evidence.
		if result.ProjectedRecords > 0 || result.OmittedRecords > 0 {
			evidence.Complete = false
		}
		return err
	})
	return evidence, err
}
func (server *Server) newCheckpointService() (*sessionrewind.Service, error) {
	files := sessionrewind.NativeFiles{ConfigHome: server.options.ConfigDir}
	return sessionrewind.New(sessionrewind.Options{
		MessagesWithEvidence: server.checkpointMessages, Snapshots: server.checkpointSnapshots,
		WorkDir: func(ctx context.Context, id string) (string, error) {
			info, err := server.findSession(ctx, id)
			if err != nil {
				return "", err
			}
			if info.WorkDir != "" {
				return info.WorkDir, nil
			}
			return server.options.WorkDir, nil
		},
		ReadBackup: files.ReadBackup, ReadCurrent: files.ReadCurrent, SafeTrackedPath: files.SafeTrackedPath, CanonicalPath: files.CanonicalPath,
		RecordedCommandIsReadOnly: server.options.RecordedCommandIsReadOnly,
		ClassifyRecordedCommand:   server.options.ClassifyRecordedCommand, Platform: runtime.GOOS,
		Files: files, StopSessionAndWait: server.stopSessionAndWait, TrimSessionMessagesFrom: server.trimSessionMessagesFrom,
	})
}
func (server *Server) turnCheckpointsHTTP(writer http.ResponseWriter, request *http.Request, info sessionInfo, parts []string) error {
	if request.Method != http.MethodGet {
		return methodNotAllowed(request.Method)
	}
	if info.ephemeral && !(len(parts) > 4 && parts[4] == "diff") {
		// Original temporary conversations have no durable message evidence or
		// backups. Listing their checkpoints never reaches a shell classifier.
		writeJSON(writer, 200, map[string]any{"checkpoints": []json.RawMessage{}})
		return nil
	}
	if !info.memory {
		stat, err := os.Stat(info.Path)
		if err != nil {
			return err
		}
		if stat.Size() > 16*1024*1024 {
			return &apiError{413, "HISTORY_CHECKPOINT_PREVIEW_LIMIT", "This transcript exceeds the full checkpoint preview budget. Chat history remains available in pages."}
		}
	}
	if server.checkpoints == nil {
		return unsupported("turn checkpoints recorded shell classifier binding")
	}
	if len(parts) > 4 && parts[4] == "diff" {
		query := request.URL.Query()
		selector := sessionrewind.Selector{TargetUserMessageID: query.Get("targetUserMessageId")}
		if raw, exists := query["userMessageIndex"]; exists && len(raw) > 0 {
			if index, valid := rewindQueryIndex(raw[0]); valid {
				selector.UserMessageIndex = &index
			}
		}
		if selector.TargetUserMessageID == "" && selector.UserMessageIndex == nil {
			return badRequest("targetUserMessageId (string) or userMessageIndex (integer) is required")
		}
		path := query.Get("path")
		if path == "" {
			return badRequest("path query parameter is required for turn checkpoint diff")
		}
		result, err := server.checkpoints.CheckpointDiff(request.Context(), info.ID, selector, path, query.Get("frozen") == "true")
		if err != nil {
			return checkpointError(err)
		}
		writeJSON(writer, 200, result)
		return nil
	}
	result, err := server.checkpoints.List(requestSignalContext(request), info.ID, request.URL.Query().Get("frozen") == "true")
	if err != nil {
		return checkpointError(err)
	}
	for _, checkpoint := range result {
		for _, path := range checkpoint.Code.FilesChanged {
			server.RegisterChangedFileAccessRoot(path, checkpoint.WorkDir)
		}
	}
	writeJSON(writer, 200, map[string]any{"checkpoints": result})
	return nil
}
func rewindQueryIndex(raw string) (int, bool) {
	prefix := parseIntPrefix.FindString(trimJS(raw))
	if prefix == "" {
		return 0, false
	}
	value, err := strconv.ParseFloat(prefix, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, false
	}
	maximum := int(^uint(0) >> 1)
	if value >= float64(maximum) {
		return maximum, true
	}
	if value < float64(-maximum) {
		return -maximum, true
	}
	return int(value), true
}
func checkpointError(err error) error {
	if errors.Is(err, sessionrewind.ErrSnapshotEvidenceIncomplete) {
		return unsupported("turn checkpoint incomplete file-history evidence")
	}
	var commandCapability *sessionrewind.UnsupportedRecordedCommand
	if errors.As(err, &commandCapability) {
		return unsupported("turn checkpoint recorded shell classifier: " + commandCapability.Feature)
	}
	var capability *sessionrewind.UnsupportedOperation
	if errors.As(err, &capability) {
		return unsupported("turn checkpoint: " + capability.Feature)
	}
	return err
}
