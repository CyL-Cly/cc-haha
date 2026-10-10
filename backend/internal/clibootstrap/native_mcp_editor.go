package clibootstrap

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/mcpconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/usercontext"
)

func (p *NativeProcessSettings) MCPEditor(configurations *mcpconfig.Service) (*mcpconfig.Editor, error) {
	p.mu.Lock()
	n := p.n
	global := p.global
	p.mu.Unlock()
	key := usercontext.CanonicalGitRoot(mainSettingsFiles{n: n}, filepath.EvalSymlinks, n.CWD)
	if key == "" {
		key = n.CWD
	}
	key = strings.ReplaceAll(key, "\\", "/")
	return mcpconfig.NewEditor(mcpconfig.EditorOptions{Configurations: configurations, GlobalPath: global.Path(), ProjectKey: key, SaveGlobal: func(ctx context.Context, update mcpconfig.Update) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return global.Save(cliglobalconfig.Updater(update))
	}, SaveProject: func(ctx context.Context, update mcpconfig.Update) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return global.SaveProject(key, cliglobalconfig.Updater(update))
	}, WriteProjectFile: func(ctx context.Context, path string, data []byte) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := n.check("write-mcp-config", path); err != nil {
			return err
		}
		var mode *fs.FileMode
		info, err := os.Stat(path)
		if err == nil {
			value := info.Mode()
			mode = &value
		} else if !os.IsNotExist(err) {
			return err
		}
		temp := fmt.Sprintf("%s.tmp.%d.%d", path, os.Getpid(), n.Now().UnixMilli())
		fileMode := fs.FileMode(0644)
		if mode != nil {
			fileMode = *mode
		}
		file, err := os.OpenFile(temp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, fileMode)
		if err != nil {
			return err
		}
		_, err = file.Write(data)
		if err == nil {
			err = file.Sync()
		}
		closed := file.Close()
		if err == nil {
			err = closed
		}
		if err != nil {
			return err
		}
		if mode != nil {
			err = os.Chmod(temp, *mode)
		}
		if err == nil {
			err = os.Rename(temp, path)
		}
		if err != nil {
			_ = os.Remove(temp)
		}
		return err
	}})
}
