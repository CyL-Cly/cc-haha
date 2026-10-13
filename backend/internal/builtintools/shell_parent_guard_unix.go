//go:build !windows

package builtintools

import (
	"os"
	"os/exec"
	"strconv"
)

// Frozen MAIN utils/shell/parentProcessGuard.ts. The guardian belongs to the
// supervisor's group, outside the target shell's job table. It observes the
// runtime PID rather than another inherited pipe or descriptor.
const parentProcessGuard = `
runtime_pid=$1
shift
(
  PATH="/bin:/usr/bin:$PATH"
  trap '[ -n "$!" ] && kill "$!" 2>/dev/null; wait 2>/dev/null; exit 0' TERM
  while kill -0 "$runtime_pid" 2>/dev/null && kill -0 "$$" 2>/dev/null; do
    sleep 1 &
    wait "$!"
    case $? in 126|127) exit 0 ;; esac
  done
  kill -KILL "-$$"
) </dev/null >/dev/null 2>&1 &
lifetime_guard=$!
"$@"
lifetime_status=$?
kill "$lifetime_guard" 2>/dev/null || :
wait "$lifetime_guard" 2>/dev/null || :
exit "$lifetime_status"
`

func configureShellProcess(cmd *exec.Cmd) {
	args := append([]string{"/bin/sh", "-c", parentProcessGuard, "cc-haha-shell-lifetime", strconv.Itoa(os.Getpid()), cmd.Path}, cmd.Args[1:]...)
	cmd.Path = "/bin/sh"
	cmd.Args = args
	cmd.Err = nil
	configureProcess(cmd)
}
