package sessioncontroller

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (c *Controller) TerminalMetadataOnOwner(scope *cooperative.Scope) (protocol.JSONObject, error) {
	if c == nil || c.writer == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return protocol.JSONObject{}, cooperative.ErrRestricted
	}
	c.writer.mu.Lock()
	defer c.writer.mu.Unlock()
	return c.writer.metadata.Clone(), nil
}

// SaveTerminalMetadataOnOwner ports the actual saveCustomTitle/saveAgentName/
// saveAgentColor/saveSessionTag appendEntryToFile boundary. It preserves the
// existing transcript shape and never creates a metadata-only session file.
func (c *Controller) SaveTerminalMetadataOnOwner(scope *cooperative.Scope, kind, value string) error {
	return c.SaveTerminalMetadataJSONOnOwner(scope, kind, quote(value))
}

func (c *Controller) SaveTerminalMetadataJSONOnOwner(scope *cooperative.Scope, kind string, value json.RawMessage) (returned error) {
	if c == nil || c.writer == nil || c.options.Bootstrap == nil || !scope.BelongsTo(c.options.Bootstrap.Owner()) {
		return cooperative.ErrRestricted
	}
	if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
		return err
	}
	field := ""
	switch kind {
	case "custom-title":
		field = "customTitle"
	case "agent-name":
		field = "agentName"
	case "agent-color":
		field = "agentColor"
	case "tag":
		field = "tag"
	default:
		return capability("terminal metadata entry type")
	}
	writer := c.writer
	writer.mu.Lock()
	if writer.async == nil || writer.async.closing {
		writer.mu.Unlock()
		return ErrPendingClosed
	}
	path, sid := writer.path, writer.currentSessionID()
	if path == "" {
		path = writer.target()
	}
	cache := writer.options.SettingsState.GetSessionSettingsCache()
	disabled := cache != nil && cache.Settings != nil && cache.Settings.Get("cleanupPeriodDays") != nil && string(protocol.StringifyValidatedJavaScriptJSON(cache.Settings.Get("cleanupPeriodDays"))) == "0"
	files := writer.options.Files
	writer.mu.Unlock()
	if !disabled {
		entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote(kind)}, protocol.JSONField{Name: field, Value: value}, protocol.JSONField{Name: "sessionId", Value: quote(sid)})
		line, err := entry.MarshalJSON()
		if err != nil {
			return err
		}
		file, err := files.OpenFile(path, os.O_WRONLY|os.O_APPEND|noFollowFlag(), 0)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil {
			info, err := file.Stat()
			if err == nil && !info.Mode().IsRegular() {
				err = errors.New("Refusing non-regular append target: " + path)
			}
			if err == nil {
				err = file.Chmod(0600)
			}
			if err == nil {
				err = writeAll(file, append(line, '\n'))
			}
			closeErr := file.Close()
			if err != nil {
				return err
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	writer.mu.Lock()
	writer.metadata.Set(field, value)
	writer.mu.Unlock()
	return nil
}
