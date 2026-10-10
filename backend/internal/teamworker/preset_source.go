package teamworker

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/agentcatalog"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"os"
	"path/filepath"
)

const maxPresetBytes = 2 * 1024 * 1024

func presetSourceHash(path string) (string, error) {
	if !filepath.IsAbs(path) {
		return "", errors.New("Agent preset source path must be absolute")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxPresetBytes {
		return "", errors.New("Agent preset source is unavailable or too large")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if len(data) > maxPresetBytes {
		return "", errors.New("Agent preset source is too large")
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}
func SnapshotPresetSource(agent agentcatalog.Definition) (protocol.JSONObject, error) {
	if agent.Source == "built-in" {
		return protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("builtin")}), nil
	}
	if agent.Source == "flagSettings" {
		return protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("session")}), nil
	}
	if agent.FilePath == "" {
		return protocol.JSONObject{}, errors.New("Agent preset has no verifiable source. Reload the agent and propose the team again.")
	}
	if agent.ContentHash == "" {
		return protocol.JSONObject{}, errors.New("Agent preset changed since it was loaded. Reload the preset and propose the team again.")
	}
	hash, err := presetSourceHash(agent.FilePath)
	if err != nil {
		return protocol.JSONObject{}, err
	}
	if hash != agent.ContentHash {
		return protocol.JSONObject{}, errors.New("Agent preset changed since it was loaded. Reload the preset and propose the team again.")
	}
	return protocol.NewJSONObject(protocol.JSONField{Name: "kind", Value: raw("file")}, protocol.JSONField{Name: "path", Value: raw(agent.FilePath)}, protocol.JSONField{Name: "sha256", Value: raw(agent.ContentHash)}), nil
}
func ValidatePresetSource(snapshot protocol.JSONObject) error {
	var identity protocol.JSONObject
	_ = identity.UnmarshalJSON(snapshot.Get("sourceIdentity"))
	if !identity.IsObject() {
		return errors.New("Agent preset source is missing. Propose the team again before approval.")
	}
	kind, source := identity.String("kind"), snapshot.String("source")
	if kind == "builtin" && source == "built-in" || kind == "session" && source == "flagSettings" {
		return nil
	}
	if kind != "file" || source == "built-in" || source == "flagSettings" {
		return errors.New("Agent preset source identity is invalid")
	}
	hash, err := presetSourceHash(identity.String("path"))
	if err != nil {
		return errors.New("Agent preset source is unavailable. Reload the preset and propose the team again.")
	}
	if hash == identity.String("sha256") {
		return nil
	}
	return errors.New("Agent preset changed after planning. Reload the preset and propose the team again.")
}
