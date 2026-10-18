package sessioncontroller

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// The original picker passes its captured fullPath to saveCustomTitle. This
// appends to that selected file and updates current metadata only for currentSID.
func (c *Controller) SaveTerminalResumeTitleOnOwner(scope *cooperative.Scope, sid, path string, value json.RawMessage) error {
	if err := c.checkTerminalResume(scope, nil); err != nil {
		return err
	}
	if _, err := protocol.DecodeJavaScriptJSONString(value); err != nil {
		return err
	}
	entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("custom-title")}, protocol.JSONField{Name: "customTitle", Value: value}, protocol.JSONField{Name: "sessionId", Value: quote(sid)})
	line, err := entry.MarshalJSON()
	if err != nil {
		return err
	}
	cache := c.writer.options.SettingsState.GetSessionSettingsCache()
	disabled := cache != nil && cache.Settings != nil && cache.Settings.Get("cleanupPeriodDays") != nil && string(protocol.StringifyValidatedJavaScriptJSON(cache.Settings.Get("cleanupPeriodDays"))) == "0"
	if !disabled {
		file, err := c.writer.options.Files.OpenFile(path, os.O_WRONLY|os.O_APPEND|noFollowFlag(), 0)
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err == nil {
			info, failure := file.Stat()
			if failure == nil && !info.Mode().IsRegular() {
				failure = errors.New("Refusing non-regular append target: " + path)
			}
			if failure == nil {
				failure = file.Chmod(0600)
			}
			if failure == nil {
				failure = writeAll(file, append(line, '\n'))
			}
			closeErr := file.Close()
			if failure != nil {
				return failure
			}
			if closeErr != nil {
				return closeErr
			}
		}
	}
	if sid == c.SessionID() {
		c.writer.mu.Lock()
		c.writer.metadata.Set("customTitle", value)
		c.writer.mu.Unlock()
	}
	return nil
}
