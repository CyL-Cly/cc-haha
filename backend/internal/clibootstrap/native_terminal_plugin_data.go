package clibootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/toolschema"
)

func terminalPluginDataDirectorySize(ctx context.Context, path string) (float64, error) {
	bytes := float64(0)
	var walk func(string) error
	walk = func(path string) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		entries, err := os.ReadDir(path)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			full := filepath.Join(path, entry.Name())
			if entry.IsDir() {
				if err := walk(full); err != nil {
					return err
				}
			} else if stat, err := os.Stat(full); err == nil {
				bytes += float64(stat.Size())
			}
		}
		return nil
	}
	err := walk(path)
	if os.IsNotExist(err) || os.IsPermission(err) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
		return 0, nil
	}
	return bytes, err
}

func (m *MainPrint) TerminalPluginDataSize(ctx context.Context, id string) (protocol.JSONObject, error) {
	return m.terminalPluginWorker(ctx, func(worker context.Context, jobs *pluginCLIJobs) (protocol.JSONObject, error) {
		p := m.nativeSettings
		p.mu.Lock()
		bindings := p.plugins
		p.mu.Unlock()
		installed, err := bindings.installed.Session(worker)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		var entries []json.RawMessage
		_ = json.Unmarshal(mainObj(installed.Get("plugins")).Get(id), &entries)
		result := protocol.NewJSONObject(protocol.JSONField{Name: "size", Value: []byte("null")})
		if len(entries) > 1 {
			return result, nil
		}
		path := filepath.Join(bindings.directories.Primary, "data", terminalPluginDataName(id))
		bytes, err := terminalPluginDataDirectorySize(worker, path)
		if err != nil {
			return protocol.JSONObject{}, err
		}
		if bytes > 0 {
			result.Set("size", rawMain(protocol.NewJSONObject(protocol.JSONField{Name: "bytes", Value: rawMain(bytes)}, protocol.JSONField{Name: "human", Value: quoteMain(toolschema.FormatFileSize(bytes))}, protocol.JSONField{Name: "path", Value: quoteMain(path)})))
		}
		return result, nil
	})
}
