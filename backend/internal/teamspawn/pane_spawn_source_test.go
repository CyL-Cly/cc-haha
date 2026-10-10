package teamspawn

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

const paneFixtureBinary = `#!/usr/bin/python3
import os,sys,json
args=sys.argv[1:]
with open(os.environ['CC_HAHA_PANE_LOG'],'a') as f: f.write(json.dumps(args)+'\n')
if args[:1]==['-L']: args=args[2:]
path=os.environ['CC_HAHA_PANE_COUNT']
try:
 with open(path) as f: count=int(f.read())
except FileNotFoundError: count=1 if os.environ.get('TMUX') else 0
cmd=args[0]
if cmd=='has-session': sys.exit(0 if count else 1)
if cmd in ['new-session','split-window'] or args[:2]==['session','split']:
 old=count;count+=1
 with open(path,'w') as f: f.write(str(count))
 print(('Created new pane: private-session-' if args[:1]==['session'] else '%')+str(old))
elif cmd=='display-message': print('%0' if args[-1]=='#{pane_id}' else 'private:0')
elif cmd=='list-panes': print('\n'.join('%'+str(i) for i in range(count)))
elif cmd=='list-windows': print('swarm-view')
`

func TestActualPaneSplitLayoutAndSendCommandsMatchSourcePhysicalSubprocess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX physical scripts; native batch quote tested separately")
	}
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	for _, profile := range []struct {
		backend string
		inside  bool
	}{{"tmux", false}, {"tmux", true}, {"iterm2", false}} {
		t.Run(profile.backend+map[bool]string{true: "-inside", false: "-outside"}[profile.inside], func(t *testing.T) {
			home := t.TempDir()
			directory := filepath.Join(home, "bin")
			log := filepath.Join(home, "args")
			_ = os.Mkdir(directory, 0700)
			for _, binary := range []string{"tmux", "it2"} {
				_ = os.WriteFile(filepath.Join(directory, binary), []byte(paneFixtureBinary), 0755)
			}
			env := map[string]string{"HOME": home, "PATH": directory + ":/usr/bin:/bin", "CC_HAHA_PANE_LOG": log, "CC_HAHA_PANE_COUNT": filepath.Join(home, "count"), "ITERM_SESSION_ID": "w0t0p0:original-leader-session"}
			if profile.inside {
				env["TMUX"] = "private-launch"
				env["TMUX_PANE"] = "%0"
			}
			service, err := NewPaneRuntime(home, os.Getpid(), env)
			if err != nil {
				t.Fatal(err)
			}
			rows := []map[string]any{}
			command := "cd 'private literal directory' && private-cli --agent-id worker@team"
			for _, name := range []string{"first", "second", "third"} {
				pane, first, err := service.CreatePane(profile.backend, name, "purple")
				if err != nil {
					t.Fatal(err)
				}
				if err = service.SendCommand(profile.backend, pane, command); err != nil {
					t.Fatal(err)
				}
				rows = append(rows, map[string]any{"paneId": pane, "isFirstTeammate": first})
			}
			data, _ := os.ReadFile(log)
			actual := [][]string{}
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var args []string
				_ = json.Unmarshal([]byte(line), &args)
				for i, value := range args {
					if strings.HasPrefix(value, "claude-swarm-") {
						args[i] = "claude-swarm-PID"
					}
				}
				actual = append(actual, args)
			}
			sourceHome, _ := filepath.EvalSymlinks(t.TempDir())
			fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-team-pane-spawn.fixture.ts")
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
			cmd.Dir = sourceHome
			cmd.Env = []string{"HOME=" + sourceHome, "USERPROFILE=" + sourceHome, "CLAUDE_CONFIG_DIR=" + filepath.Join(sourceHome, ".claude"), "TMPDIR=" + sourceHome, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(sourceHome), "CC_HAHA_TEAM_PANE_FIXTURE=1", "NODE_ENV=production", "BUN_OPTIONS=--no-env-file", "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "PATH=/usr/bin:/bin"}
			cmd.Stdin = bytes.NewReader(rawJSON(map[string]any{"homeDir": sourceHome, "backend": profile.backend, "inside": profile.inside, "command": command, "names": []string{"first", "second", "third"}, "binaryScript": paneFixtureBinary}))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if err != nil {
				t.Fatal(err, stderr.String(), string(out))
			}
			var source struct {
				Rows         []map[string]any
				Args         [][]string
				Capabilities struct{ Blocked []string }
			}
			if err = json.Unmarshal(out, &source); err != nil || len(source.Capabilities.Blocked) > 0 {
				t.Fatal(err, string(out))
			}
			if !reflect.DeepEqual(source.Rows, rows) || !reflect.DeepEqual(source.Args, actual) {
				t.Fatalf("Source differs\nrows Source=%v Go=%v\nargs Source=%v\nGo=%v", source.Rows, rows, source.Args, actual)
			}
		})
	}
}
