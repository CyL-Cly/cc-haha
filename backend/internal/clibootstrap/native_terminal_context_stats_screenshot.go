package clibootstrap

import (
	"context"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

func (m *MainPrint) CopyTerminalStatsScreenshot(ctx context.Context, text string) error {
	receipt, err := m.Owner.SubmitIndependent(ctx, cooperative.Read, func(scope *cooperative.Scope) error {
		if m.closed {
			return cooperative.ErrClosed
		}
		future, err := nativeSkillWork(m, scope, func() (struct{}, error) {
			n := m.options.Native
			png, err := tui.StatsScreenshotPNG(text)
			if err != nil {
				return struct{}{}, err
			}
			directory := filepath.Join(n.Temp, "claude-code-screenshots")
			if err = n.check("write", directory); err != nil {
				return struct{}{}, err
			}
			if err = os.MkdirAll(directory, 0700); err != nil {
				return struct{}{}, err
			}
			path := filepath.Join(directory, fmt.Sprintf("screenshot-%d.png", n.Now().UnixMilli()))
			if err = n.check("write", path); err != nil {
				return struct{}{}, err
			}
			if err = os.WriteFile(path, png, 0600); err != nil {
				return struct{}{}, err
			}
			defer os.Remove(path)
			environment := []string{}
			keys := []string{}
			for key := range n.Environment {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			for _, key := range keys {
				environment = append(environment, key+"="+n.Environment[key])
			}
			run := func(name string, args ...string) error {
				commandCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				defer cancel()
				// Resolve only through this process's explicit environment, never the
				// host test runner's PATH or a shell command containing user text.
				executable := ""
				for _, directory := range filepath.SplitList(n.Environment["PATH"]) {
					candidate := filepath.Join(directory, name)
					info, e := os.Stat(candidate)
					if e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
						executable = candidate
						break
					}
				}
				if executable == "" {
					return fmt.Errorf("clipboard executable missing: %s", name)
				}
				command := exec.CommandContext(commandCtx, executable, args...)
				command.Dir = n.CWD
				command.Env = environment
				output, e := command.CombinedOutput()
				if e != nil {
					return fmt.Errorf("clipboard %s: %w: %s", name, e, strings.TrimSpace(string(output)))
				}
				return nil
			}
			switch m.options.Platform {
			case "darwin":
				escaped := strings.ReplaceAll(strings.ReplaceAll(path, "\\", "\\\\"), "\"", "\\\"")
				err = run("osascript", "-e", "set the clipboard to (read (POSIX file \""+escaped+"\") as «class PNGf»)")
			case "linux":
				err = run("xclip", "-selection", "clipboard", "-t", "image/png", "-i", path)
				if err != nil {
					err = run("xsel", "--clipboard", "--input", "--type", "image/png")
				}
			case "windows":
				err = run("powershell", "-NoProfile", "-Command", "Add-Type -AssemblyName System.Windows.Forms; [System.Windows.Forms.Clipboard]::SetImage([System.Drawing.Image]::FromFile('"+strings.ReplaceAll(path, "'", "''")+"'))")
			default:
				err = fmt.Errorf("Screenshot to clipboard is not supported on %s", m.options.Platform)
			}
			return struct{}{}, err
		})
		if err != nil {
			return err
		}
		_, err = future.Await(scope)
		return err
	})
	if err == nil {
		err = receipt.Wait()
	}
	return err
}
