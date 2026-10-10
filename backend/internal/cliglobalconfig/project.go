package cliglobalconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

var ErrProjectShape = errors.New("unimplemented CLI project config shape")

func projectDefaults() *protocol.JSONObject {
	object := protocol.NewJSONObject()
	for _, field := range []struct{ name, value string }{{"allowedTools", "[]"}, {"mcpContextUris", "[]"}, {"mcpServers", "{}"}, {"enabledMcpjsonServers", "[]"}, {"disabledMcpjsonServers", "[]"}, {"hasTrustDialogAccepted", "false"}, {"projectOnboardingSeenCount", "0"}, {"hasClaudeMdExternalIncludesApproved", "false"}, {"hasClaudeMdExternalIncludesWarningShown", "false"}} {
		object.Set(field.name, []byte(field.value))
	}
	return &object
}

// ResolvedKey is the host's canonical Git/project path after Source path
// normalization. This service does not replace findCanonicalGitRoot.
func (service *Service) GetProject(resolvedKey string) (*protocol.JSONObject, error) {
	if resolvedKey == "" {
		return nil, errors.New("CLI project config requires an explicit resolved project key")
	}
	global, err := service.Get()
	if err != nil {
		return nil, err
	}
	projects := global.Get("projects")
	if !jsTruthy(projects) {
		return projectDefaults(), nil
	}
	var collection protocol.JSONObject
	if collection.UnmarshalJSON(projects) != nil || !collection.IsObject() {
		return nil, ErrProjectShape
	}
	raw := collection.Get(resolvedKey)
	if raw == nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return projectDefaults(), nil
	}
	var config protocol.JSONObject
	if config.UnmarshalJSON(raw) != nil || !config.IsObject() {
		return nil, ErrProjectShape
	}
	allowed := bytes.TrimSpace(config.Get("allowedTools"))
	if len(allowed) > 0 && allowed[0] == '"' {
		units, err := protocol.DecodeJavaScriptJSONString(allowed)
		if err != nil {
			return nil, err
		}
		text := protocol.JavaScriptJSONSourceUTF16(units)
		next := json.RawMessage(text)
		if _, invalid := provider.PinnedJavaScriptJSONSyntaxMessage(text); invalid || !json.Valid(next) || bytes.Equal(bytes.TrimSpace(next), []byte("null")) {
			next = []byte("[]")
		}
		config.Set("allowedTools", next)
		updated, _ := config.MarshalJSON()
		collection.Set(resolvedKey, updated)
		updated, _ = collection.MarshalJSON()
		global.Set("projects", updated)
	}
	return &config, nil
}
func (service *Service) SaveProject(resolvedKey string, updater Updater) error {
	if resolvedKey == "" || updater == nil {
		return errors.New("CLI project config requires a resolved key and updater")
	}
	service.saveMu.Lock()
	defer service.saveMu.Unlock()
	return service.save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
		projects := protocol.NewJSONObject()
		raw := current.Get("projects")
		if jsTruthy(raw) && (projects.UnmarshalJSON(raw) != nil || !projects.IsObject()) {
			return nil, ErrProjectShape
		}
		project := projectDefaults()
		if raw := projects.Get(resolvedKey); raw != nil && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			var decoded protocol.JSONObject
			if decoded.UnmarshalJSON(raw) != nil || !decoded.IsObject() {
				return nil, ErrProjectShape
			}
			project = &decoded
		}
		next, err := updater(project)
		if err != nil {
			return nil, err
		}
		if next == project {
			return current, nil
		}
		if next == nil {
			return nil, ErrProjectShape
		}
		encoded, err := next.MarshalJSON()
		if err != nil {
			return nil, err
		}
		projects.Set(resolvedKey, encoded)
		encoded, err = projects.MarshalJSON()
		if err != nil {
			return nil, err
		}
		global := current.Clone()
		global.Set("projects", encoded)
		return &global, nil
	})
}
