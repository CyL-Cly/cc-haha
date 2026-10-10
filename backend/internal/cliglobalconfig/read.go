package cliglobalconfig

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf16"
)

type ConfigParseError struct {
	FilePath      string
	MessageJSON   json.RawMessage
	DefaultConfig *protocol.JSONObject
}

func (failure *ConfigParseError) Error() string {
	units, _ := protocol.DecodeJavaScriptJSONString(failure.MessageJSON)
	return string(utf16.Decode(units))
}
func (service *Service) read(strict bool) (*protocol.JSONObject, error) {
	service.stateMu.Lock()
	allowed := service.enabled
	service.stateMu.Unlock()
	if !allowed {
		return nil, errors.New("Config accessed before allowed.")
	}
	path := service.Path()
	raw, err := service.options.FS.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if backup := service.latestBackup(); backup != "" {
				service.options.Stderr(fmt.Sprintf("\nClaude configuration file not found at: %s\nA backup file exists at: %s\nYou can manually restore it by running: cp \"%s\" \"%s\"\n\n", path, backup, backup, path))
			}
		}
		return DefaultFactory(), nil
	}
	config, err := parseSpread(raw, false)
	if err == nil {
		return config, nil
	}
	var failure *ConfigParseError
	if !errors.As(err, &failure) {
		return DefaultFactory(), nil
	}
	failure.FilePath = path
	if strict {
		return nil, failure
	}
	service.stateMu.Lock()
	outer := !service.insideRead
	service.insideRead = true
	service.stateMu.Unlock()
	if outer {
		service.emit("parse_error", map[string]any{"file": path})
		service.stateMu.Lock()
		service.insideRead = false
		service.stateMu.Unlock()
	}
	service.options.Stderr(fmt.Sprintf("\nClaude configuration file at %s is corrupted: %s\n", path, failure.Error()))
	directory := filepath.Join(service.configHome, "backups")
	if err := service.options.FS.MkdirAll(directory); err != nil && !errors.Is(err, fs.ErrExist) {
		return nil, err
	}
	entries, err := service.options.FS.ReadDir(directory)
	if err != nil {
		return nil, err
	}
	prefix := filepath.Base(path) + ".corrupted."
	current, err := service.options.FS.ReadFile(path)
	if err != nil {
		return nil, err
	}
	already := false
	currentText := string(storage.DecodeUTF8(current))
	for _, name := range entries {
		if !strings.HasPrefix(name, prefix) {
			continue
		}
		previous, readErr := service.options.FS.ReadFile(filepath.Join(directory, name))
		if readErr == nil && currentText == string(storage.DecodeUTF8(previous)) {
			already = true
			break
		}
	}
	corrupted := ""
	if !already {
		corrupted = filepath.Join(directory, fmt.Sprintf("%s%d", prefix, service.options.Now().UnixMilli()))
		_ = service.options.FS.CopyFile(path, corrupted)
	}
	backup := service.latestBackup()
	if corrupted != "" {
		service.options.Stderr("The corrupted file has been backed up to: " + corrupted + "\n")
	} else if already {
		service.options.Stderr("The corrupted file has already been backed up.\n")
	}
	if backup != "" {
		service.options.Stderr(fmt.Sprintf("A backup file exists at: %s\nYou can manually restore it by running: cp \"%s\" \"%s\"\n\n", backup, backup, path))
	} else {
		service.options.Stderr("\n")
	}
	return DefaultFactory(), nil
}
func (service *Service) latestBackup() string {
	path := service.Path()
	prefix := filepath.Base(path) + ".backup."
	for _, directory := range []string{filepath.Join(service.configHome, "backups"), filepath.Dir(path)} {
		entries, err := service.options.FS.ReadDir(directory)
		if err != nil {
			continue
		}
		names := []string{}
		for _, name := range entries {
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		sort.Slice(names, func(i, j int) bool { return lessUTF16(names[i], names[j]) })
		if len(names) > 0 {
			return filepath.Join(directory, names[len(names)-1])
		}
	}
	if _, err := service.options.FS.Stat(path + ".backup"); err == nil {
		return path + ".backup"
	}
	return ""
}
