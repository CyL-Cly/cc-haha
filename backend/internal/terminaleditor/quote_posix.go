//go:build !windows

package terminaleditor

import "strings"

func quotePath(path string) string { return "'" + strings.ReplaceAll(path, "'", "'\"'\"'") + "'" }
