package sdkpermissions

import (
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"syscall"
)

// ValidateWorkspaceDirectory reuses native startup's exact path expansion and
// working-path comparison, with Source's single stat and error classification.
func (s *NativeStartup) ValidateWorkspaceDirectory(path string, permissionContext protocol.JSONObject) (protocol.JSONObject, error) {
	return s.ValidateWorkspaceDirectoryAt(path, permissionContext, s.options.CWD, s.options.OriginalCWD)
}

func (s *NativeStartup) ValidateWorkspaceDirectoryAt(path string, permissionContext protocol.JSONObject, cwd, originalCWD string) (protocol.JSONObject, error) {
	current := *s
	current.options.CWD, current.options.OriginalCWD = cwd, originalCWD
	return current.validateWorkspaceDirectory(path, permissionContext)
}

func (s *NativeStartup) validateWorkspaceDirectory(path string, permissionContext protocol.JSONObject) (protocol.JSONObject, error) {
	record := func(kind string, fields ...protocol.JSONField) protocol.JSONObject {
		return protocol.NewJSONObject(append([]protocol.JSONField{{Name: "resultType", Value: quote(kind)}}, fields...)...)
	}
	if path == "" {
		return record("emptyPath"), nil
	}
	absolute, err := s.expand(path)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	fields := []protocol.JSONField{{Name: "directoryPath", Value: quote(path)}, {Name: "absolutePath", Value: quote(absolute)}}
	info, err := s.options.Stat(absolute)
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) || errors.Is(err, syscall.ENOTDIR) {
			return record("pathNotFound", fields...), nil
		}
		return protocol.JSONObject{}, err
	}
	if !info.IsDir() {
		return record("notADirectory", fields...), nil
	}
	working := []string{s.options.OriginalCWD}
	for _, directory := range WorkspaceDirectoryEntries(permissionContext) {
		working = append(working, directory.String("path"))
	}
	for _, root := range working {
		if s.workingPath(absolute, root) {
			return record("alreadyInWorkingDirectory", protocol.JSONField{Name: "directoryPath", Value: quote(path)}, protocol.JSONField{Name: "workingDir", Value: quote(root)}), nil
		}
	}
	return record("success", protocol.JSONField{Name: "absolutePath", Value: quote(absolute)}), nil
}
func WorkspaceDirectoryEntries(permissionContext protocol.JSONObject) []protocol.JSONObject {
	var collection protocol.JSONObject
	_ = collection.UnmarshalJSON(permissionContext.Get("additionalWorkingDirectories"))
	var entries [][]json.RawMessage
	_ = json.Unmarshal(collection.Get("map"), &entries)
	result := []protocol.JSONObject{}
	for _, entry := range entries {
		if len(entry) < 2 {
			continue
		}
		var row protocol.JSONObject
		if row.UnmarshalJSON(entry[1]) != nil {
			continue
		}
		row = row.Clone()
		row.Set("path", entry[0])
		result = append(result, row)
	}
	return result
}
