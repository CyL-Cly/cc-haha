package cliinstallation

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"golang.org/x/text/unicode/norm"
)

type CommandResult struct {
	Code           int
	Stdout, Stderr string
}
type SetupMessage struct {
	Message            string
	UserActionRequired bool
	Type               string
}
type LegacyCleanup struct {
	Removed          int
	Errors, Warnings []string
	Aliases          []SetupMessage
}

const jsSpacePattern = `[\x09-\x0d\x20\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var aliasStart = regexp.MustCompile(`^` + jsSpacePattern + `*alias` + jsSpacePattern + `+claude` + jsSpacePattern + `*=`)
var aliasQuoted = regexp.MustCompile(`alias` + jsSpacePattern + `+claude` + jsSpacePattern + `*=` + jsSpacePattern + `*["']([^"']+)["']`)
var aliasPlain = regexp.MustCompile(`alias` + jsSpacePattern + `+claude` + jsSpacePattern + `*=` + jsSpacePattern + `*([^#\n]+)`)

// FilterLegacyAliases removes only the exact installer-created target. User
// aliases, including unexpanded $HOME/~ and custom targets, remain intact.
func FilterLegacyAliases(lines []string, localExecutable string) ([]string, bool) {
	out := make([]string, 0, len(lines))
	found := false
	for _, line := range lines {
		remove := false
		if aliasStart.MatchString(line) {
			match := aliasQuoted.FindStringSubmatch(line)
			if match == nil {
				match = aliasPlain.FindStringSubmatch(line)
			}
			remove = len(match) > 1 && protocol.TrimJavaScriptString(match[1]) == localExecutable
		}
		if remove {
			found = true
		} else {
			out = append(out, line)
		}
	}
	return out, found
}

func (s *Installer) CleanupShellAliases() []SetupMessage {
	config, explicit := s.options.Environment["CLAUDE_CONFIG_DIR"]
	if !explicit {
		config = filepath.Join(s.options.Home, ".claude")
	}
	config = norm.NFC.String(config)
	zsh := s.options.Environment["ZDOTDIR"]
	if zsh == "" {
		zsh = s.options.Home
	}
	paths := []string{filepath.Join(zsh, ".zshrc"), filepath.Join(s.options.Home, ".bashrc"), filepath.Join(s.options.Home, ".config", "fish", "config.fish")}
	messages := []SetupMessage{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if os.IsNotExist(err) || os.IsPermission(err) || errors.Is(err, syscall.ENOTDIR) || errors.Is(err, syscall.ELOOP) {
			continue
		}
		if err == nil {
			lines, found := FilterLegacyAliases(strings.Split(string(protocol.DecodeJavaScriptUTF8(data)), "\n"), filepath.Join(config, "local", "claude"))
			if !found {
				continue
			}
			var file *os.File
			file, err = os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0666)
			if err == nil {
				_, err = file.Write([]byte(strings.Join(lines, "\n")))
				if err == nil {
					err = file.Sync()
				}
				closeErr := file.Close()
				if err == nil {
					err = closeErr
				}
			}
			if err == nil {
				messages = append(messages, SetupMessage{"Removed claude alias from " + path + ". Run: unalias claude", true, "alias"})
				continue
			}
		}
		messages = append(messages, SetupMessage{"Failed to clean up " + path + ": " + storage.JavaScriptErrorText(err), false, "error"})
	}
	return messages
}

func (s *Installer) runLegacy(ctx context.Context, arguments []string) (CommandResult, error) {
	if s.options.Run != nil {
		return s.options.Run(ctx, "npm", arguments, s.options.CWD)
	}
	return runLegacyCommand(ctx, "npm", arguments, s.options.CWD, s.options.Home, s.options.Environment)
}
func (s *Installer) manualRemoveNPM(ctx context.Context, name string) (bool, string, string) {
	result, err := s.runLegacy(ctx, []string{"config", "get", "prefix"})
	if err != nil {
		return false, "Manual removal failed: " + err.Error(), ""
	}
	if result.Code != 0 || result.Stdout == "" {
		return false, "Failed to get npm global prefix", ""
	}
	prefix := protocol.TrimJavaScriptString(result.Stdout)
	paths := []string{filepath.Join(prefix, "bin", "claude")}
	modules := filepath.Join(prefix, "lib", "node_modules", name)
	if s.options.Platform == "windows" || s.options.Platform == "win32" {
		paths = []string{filepath.Join(prefix, "claude.cmd"), filepath.Join(prefix, "claude.ps1"), filepath.Join(prefix, "claude")}
		modules = filepath.Join(prefix, "node_modules", name)
	}
	removed := false
	for _, p := range paths {
		if unlinkLegacy(p) == nil {
			removed = true
		}
	}
	if removed {
		return true, "", name + " executables removed, but node_modules directory was left intact for safety. You may manually delete it later at: " + modules
	}
	return false, "", ""
}
func (s *Installer) CleanupNPM(ctx context.Context) LegacyCleanup {
	result := LegacyCleanup{Errors: []string{}, Warnings: []string{}, Aliases: []SetupMessage{}}
	packages := []string{"@anthropic-ai/claude-code"}
	if name := s.options.PackageName; name != "" && name != packages[0] {
		packages = append(packages, name)
	}
	for _, name := range packages {
		command, err := s.runLegacy(ctx, []string{"uninstall", "-g", name})
		if err != nil {
			result.Errors = append(result.Errors, err.Error())
			continue
		}
		if command.Code == 0 {
			result.Removed++
			continue
		}
		if command.Stderr == "" || strings.Contains(command.Stderr, "npm ERR! code E404") {
			continue
		}
		message := "Failed to remove global npm installation of " + name + ": " + command.Stderr
		if strings.Contains(command.Stderr, "npm error code ENOTEMPTY") {
			removed, failure, warning := s.manualRemoveNPM(ctx, name)
			if removed {
				result.Removed++
				if warning != "" {
					result.Warnings = append(result.Warnings, warning)
				}
				continue
			}
			if failure != "" {
				message += ". Manual removal also failed: " + failure
			}
		}
		result.Errors = append(result.Errors, message)
	}
	// Source local NPM cleanup deliberately uses HOME/.claude/local even when
	// the active settings directory was relocated. Package data stays otherwise.
	local := filepath.Join(s.options.Home, ".claude", "local")
	if _, err := os.Lstat(local); err == nil {
		if err = os.RemoveAll(local); err == nil {
			result.Removed++
		} else {
			result.Errors = append(result.Errors, "Failed to remove "+local+": "+storage.JavaScriptErrorText(err))
		}
	} else if !os.IsNotExist(err) {
		result.Errors = append(result.Errors, "Failed to remove "+local+": "+storage.JavaScriptErrorText(err))
	}
	return result
}
