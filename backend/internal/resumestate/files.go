package resumestate

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type OSFiles struct{}

func (OSFiles) Open(path string) (sessionstore.File, error) {
	return (sessionstore.OSFileSystem{}).Open(path)
}
func (OSFiles) ReadFile(path string) ([]byte, error)       { return os.ReadFile(path) }
func (OSFiles) WriteFile(path string, data []byte) error   { return os.WriteFile(path, data, 0666) }
func (OSFiles) MkdirAll(path string) error                 { return os.MkdirAll(path, 0777) }
func (OSFiles) Link(old, new string) error                 { return os.Link(old, new) }
func (OSFiles) ReadDir(path string) ([]fs.DirEntry, error) { return os.ReadDir(path) }
func (OSFiles) CopyFile(old, new string) (err error) {
	input, err := os.Open(old)
	if err != nil {
		return err
	}
	defer input.Close()
	info, err := input.Stat()
	if err != nil {
		return err
	}
	output, err := os.OpenFile(new, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return err
	}
	defer func() {
		closed := output.Close()
		if err == nil {
			err = closed
		}
	}()
	_, err = io.Copy(output, input)
	return err
}
func safeSegment(raw json.RawMessage, label string) (string, error) {
	value, valid := units(raw)
	if !valid {
		return "", &CapabilityError{Capability: "non-string file-history " + label}
	}
	part := text(raw)
	if len(value) == 0 || part == "." || part == ".." || strings.ContainsAny(part, "/\\") {
		return "", failure("FileHistory: Refusing unsafe %s: %s", label, part)
	}
	return part, nil
}
func (service *Service) startFileHistory(log protocol.JSONObject) {
	if !envTruthy(service.options.Environment["CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING"]) || envTruthy(service.options.Environment["CLAUDE_CODE_DISABLE_FILE_CHECKPOINTING"]) {
		return
	}
	snapshots, exists := array(log.Get("fileHistorySnapshots"))
	history, _ := array(log.Get("messages"))
	if !exists || len(history) == 0 {
		return
	}
	previous := object(history[len(history)-1]).Get("sessionId")
	if !truthy(previous) {
		service.options.OnError(failure("FileHistory: Failed to copy backups on restore (no previous session id)"))
		return
	}
	if scalarEqual(previous, quote(service.options.CurrentSessionID)) {
		service.diagnostic("debug", "FileHistory: No need to copy file history for resuming with same session id: "+service.options.CurrentSessionID, nil)
		return
	}
	service.background.Add(1)
	go func() { defer service.background.Done(); service.copyFileHistory(snapshots, previous) }()
}
func (service *Service) copyFileHistory(snapshots []json.RawMessage, previous json.RawMessage) {
	directory := filepath.Join(service.options.ConfigDir, "file-history", service.options.CurrentSessionID)
	if err := service.options.Files.MkdirAll(directory); err != nil {
		service.options.OnError(err)
		return
	}
	var snapshotsWait sync.WaitGroup
	var countMu sync.Mutex
	failed := 0
	for _, snapshot := range snapshots {
		snapshotsWait.Add(1)
		go func(snapshot json.RawMessage) {
			defer snapshotsWait.Done()
			value := object(snapshot)
			backups := []json.RawMessage{}
			tracked := object(value.Get("trackedFileBackups"))
			if !tracked.IsObject() {
				return
			}
			for _, field := range members(tracked) {
				backups = append(backups, field.Value)
			}
			for _, field := range members(object(value.Get("completedFileBackups"))) {
				backups = append(backups, field.Value)
			}
			var wait sync.WaitGroup
			var mu sync.Mutex
			copyFailed := false
			for _, backup := range backups {
				name := object(backup).Get("backupFileName")
				if isNull(name) {
					continue
				}
				wait.Add(1)
				go func(name json.RawMessage) {
					defer wait.Done()
					if err := service.copyBackup(directory, previous, name); err != nil {
						mu.Lock()
						copyFailed = true
						mu.Unlock()
					}
				}(name)
			}
			wait.Wait()
			if copyFailed {
				countMu.Lock()
				failed++
				countMu.Unlock()
				return
			}
			settings, _, err := service.settings()
			if err != nil {
				service.options.OnError(failure("FileHistory: Failed to record copy backup snapshot"))
				return
			}
			if scalarEqual(settings.Get("cleanupPeriodDays"), json.RawMessage("0")) || envTruthy(service.options.Environment["CLAUDE_CODE_SKIP_PROMPT_HISTORY"]) {
				return
			}
			entry := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quote("file-history-snapshot")}, protocol.JSONField{Name: "messageId", Value: value.Get("messageId")}, protocol.JSONField{Name: "snapshot", Value: snapshot}, protocol.JSONField{Name: "isSnapshotUpdate", Value: []byte("false")})
			// The genuine Project.appendEntry startup state buffers until a user or
			// assistant materializes the new transcript. No metadata-only file is made.
			if service.options.OnSnapshotRecord != nil {
				if err := service.options.OnSnapshotRecord(entry); err != nil {
					service.options.OnError(failure("FileHistory: Failed to record copy backup snapshot"))
					return
				}
			}
			service.mu.Lock()
			service.state.PendingEntries = append(service.state.PendingEntries, encode(entry))
			service.mu.Unlock()
		}(snapshot)
	}
	snapshotsWait.Wait()
	if failed > 0 {
		service.diagnostic("event", "tengu_file_history_resume_copy_failed", encode(protocol.NewJSONObject(protocol.JSONField{Name: "numSnapshots", Value: encode(len(snapshots))}, protocol.JSONField{Name: "failedSnapshots", Value: encode(failed)})))
	}
}
func (service *Service) copyBackup(directory string, previous, name json.RawMessage) error {
	part, err := safeSegment(name, "backup file name")
	if err != nil {
		return err
	}
	sid, err := safeSegment(previous, "session ID")
	if err != nil {
		return err
	}
	old := filepath.Join(service.options.ConfigDir, "file-history", sid, part)
	new := filepath.Join(directory, part)
	if err = service.options.Files.Link(old, new); errors.Is(err, fs.ErrExist) {
		return nil
	} else if errors.Is(err, fs.ErrNotExist) {
		service.options.OnError(failure("FileHistory: Failed to copy backup %s on restore (backup file does not exist in %s)", part, sid))
		return err
	} else if err != nil {
		service.options.OnError(failure("FileHistory: Error hard linking backup file from previous session"))
		if err = service.options.Files.CopyFile(old, new); err != nil {
			service.options.OnError(failure("FileHistory: Error copying over backup from previous session"))
			return err
		}
	}
	service.diagnostic("debug", "FileHistory: Copied backup "+part+" from session "+sid+" to "+service.options.CurrentSessionID, nil)
	return nil
}
