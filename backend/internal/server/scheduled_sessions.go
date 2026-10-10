package server

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (server *Server) NotifyCanonicalTranscriptMutation(path string) {
	server.noteSessionMutation(path)
}

// CreateScheduledSession uses the ordinary desktop creation boundary. The
// dispatcher calls it only for the user's explicit Run Now operation.
func (server *Server) CreateScheduledSession(ctx context.Context, workDir, mode string) (string, error) {
	body, err := json.Marshal(map[string]string{"workDir": workDir, "permissionMode": mode})
	if err != nil {
		return "", err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://scheduler.internal/api/sessions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	created, err := server.createSession(request)
	if err != nil {
		return "", err
	}
	return created.(map[string]string)["sessionId"], nil
}

// Only the just-created placeholder is removed. The known session and its
// launch identity remain available for the shared CLI writer to materialize.
func (server *Server) DeleteScheduledPlaceholder(ctx context.Context, id string) error {
	info, err := server.findSession(ctx, id)
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.status == 404 {
			return nil
		}
		return err
	}
	if info.memory {
		return nil
	}
	if err = os.Remove(info.Path); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	server.noteSessionMutation(info.Path)
	return nil
}

func (server *Server) AppendScheduledSessionMetadata(ctx context.Context, id string, metadata protocol.JSONObject) error {
	info, err := server.findSession(ctx, id)
	if err != nil {
		var api *apiError
		if errors.As(err, &api) && api.status == 404 {
			return nil
		}
		return err
	}
	value, err := metadata.MarshalJSON()
	if err != nil {
		return err
	}
	if err = server.updateLaunchMetadata(ctx, id, value); err != nil {
		return err
	}
	persist, err := server.shouldPersist(ctx)
	if err != nil || !persist || info.memory {
		return err
	}
	if info.WorkDir == metadata.String("workDir") && info.PermissionMode == metadata.String("permissionMode") {
		return nil
	}
	current, err := server.shouldPersist(ctx)
	if err != nil || !current {
		return err
	}
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: json.RawMessage(`"session-meta"`)}, protocol.JSONField{Name: "isMeta", Value: json.RawMessage("true")}, protocol.JSONField{Name: "workDir", Value: metadata.Get("workDir")}, protocol.JSONField{Name: "permissionMode", Value: metadata.Get("permissionMode")}, protocol.JSONField{Name: "timestamp", Value: json.RawMessage(strconvQuote(time.Now().UTC().Format("2006-01-02T15:04:05.000Z")))})
	data, err := entry.MarshalJSON()
	if err != nil {
		return err
	}
	file, err := os.OpenFile(info.Path, os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		return err
	}
	_, err = file.Write(append(data, '\n'))
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err == nil {
		server.noteSessionMutation(info.Path)
	}
	return err
}
