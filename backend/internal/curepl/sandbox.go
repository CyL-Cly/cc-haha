package curepl

import (
	_ "embed"
	"errors"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Fixed essential process/IPC/sysctl rules from the same sandbox-runtime
// revision used by Source. There are no network or wildcard mach allowances.
//
//go:embed assets/sandbox-base.sb
var sandboxBase string

func SandboxProfile(executable, directory string) (string, error) {
	if !filepath.IsAbs(executable) || !filepath.IsAbs(directory) || directory == "/" {
		return "", errors.New("Computer Use kernel requires canonical executable and disposable directory")
	}
	var profile strings.Builder
	profile.WriteString(sandboxBase)
	profile.WriteString("\n(allow file-read*)\n(deny file-read* (subpath \"/\"))\n(allow file-read* (literal \"/\"))\n(allow file-read-metadata (vnode-type DIRECTORY))\n")
	for _, path := range []string{executable, directory, "/System", "/usr/lib", "/usr/share", "/bin/bash", "/usr/bin/env", "/dev", "/private/etc/localtime"} {
		profile.WriteString("(allow file-read* (subpath " + strconv.Quote(path) + "))\n")
	}
	// Same root movement block as Source's denyOnly:['/'] read configuration.
	profile.WriteString("(deny file-write-unlink (subpath \"/\"))\n")
	for _, path := range []string{directory, "/dev/null"} {
		profile.WriteString("(allow file-write* (subpath " + strconv.Quote(path) + "))\n")
	}
	for _, name := range []string{".gitconfig", ".gitmodules", ".bashrc", ".bash_profile", ".zshrc", ".zprofile", ".profile", ".ripgreprc", ".mcp.json"} {
		profile.WriteString("(deny file-write* (regex #" + strconv.Quote("(^|/)"+regexp.QuoteMeta(name)+"$") + "))\n")
	}
	for _, name := range []string{".vscode", ".idea", ".claude/commands", ".claude/agents", ".git/hooks"} {
		profile.WriteString("(deny file-write* (regex #" + strconv.Quote("(^|/)"+regexp.QuoteMeta(name)+"(/|$)") + "))\n")
	}
	profile.WriteString("(deny file-write* (regex #\"(^|/)\\\\.git/config$\"))\n")
	return profile.String(), nil
}
