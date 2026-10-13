package processpolicy

import (
	"context"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

// ResolveWindowsTaskkill follows MAIN's case-insensitive SystemRoot/windir
// lookup. PATH and the project directory never select the tree killer.
func ResolveWindowsTaskkill(environment []string) string {
	get := func(name string) string {
		for _, item := range environment {
			key, value, ok := strings.Cut(item, "=")
			if ok && value != "" && strings.EqualFold(key, name) {
				return value
			}
		}
		return ""
	}
	root := get("SystemRoot")
	if root == "" {
		root = get("windir")
	}
	if root == "" {
		return ""
	}
	root = strings.ReplaceAll(root, `\`, "/")
	unc := strings.HasPrefix(root, "//")
	result := path.Clean(root + "/System32/taskkill.exe")
	if unc && !strings.HasPrefix(result, "//") {
		result = "/" + result
	}
	return strings.ReplaceAll(result, "/", `\`)
}

type WindowsTreeKillOptions struct {
	Environment []string
	Timeout     time.Duration
	Run         func(context.Context, string, []string, []string) error
}

// KillWindowsProcessTree returns true only after taskkill acknowledged the
// whole process tree. The caller retains MAIN's direct-PID fallback on false.
func KillWindowsProcessTree(ctx context.Context, pid int, options WindowsTreeKillOptions) bool {
	if pid <= 0 {
		return false
	}
	environment := options.Environment
	if environment == nil {
		environment = os.Environ()
	}
	executable := ResolveWindowsTaskkill(environment)
	if executable == "" {
		return false
	}
	run := options.Run
	if run == nil {
		run = runWindowsTreeKiller
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	timed, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return run(timed, executable, []string{"/PID", strconv.Itoa(pid), "/T", "/F"}, environment) == nil
}
