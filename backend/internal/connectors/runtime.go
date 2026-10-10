package connectors

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	_ "embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed data/native-pins.json
var nativePinsJSON []byte

type ArtifactPins struct {
	Archive string `json:"archiveIntegrity"`
	Binary  string `json:"binaryIntegrity"`
}
type ProcessResult struct {
	Code           int
	Stdout, Stderr string
}
type RunOptions struct {
	Environment map[string]string
	Timeout     time.Duration
	OnOutput    func(string)
}
type Runtime struct {
	Platform, Arch string
	Environment    map[string]string
	Download       func(context.Context, []string, string) ([]byte, error)
	Extract        func(context.Context, string, string) ([]byte, error)
	Run            func(context.Context, string, []string, RunOptions) (ProcessResult, error)
	ReadBinary     func(string) ([]byte, error)
	Pins           func(protocol.JSONObject, string) (ArtifactPins, error)
}

func DefaultRuntime(platform, arch string, env map[string]string) Runtime {
	r := Runtime{Platform: platform, Arch: arch, Environment: env, Download: DownloadVerified, ReadBinary: os.ReadFile, Pins: defaultPins}
	r.Run = func(ctx context.Context, command string, args []string, o RunOptions) (ProcessResult, error) {
		a, b, c, e := runCaptured(ctx, command, args, o, 256*1024, env)
		return ProcessResult{c, string(a), string(b)}, e
	}
	r.Extract = func(ctx context.Context, archive, member string) ([]byte, error) {
		out, _, code, err := runCaptured(ctx, "tar", []string{"-xOf", archive, member}, RunOptions{Timeout: 60 * time.Second}, 128*1024*1024, env)
		if err != nil {
			return nil, err
		}
		if code != 0 {
			return nil, errors.New("Cannot extract connector runtime (tar required)")
		}
		return out, nil
	}
	return r
}

func defaultPins(d protocol.JSONObject, target string) (ArtifactPins, error) {
	var all map[string]map[string]map[string]ArtifactPins
	if err := json.Unmarshal(nativePinsJSON, &all); err != nil {
		return ArtifactPins{}, err
	}
	pins := all[d.String("id")][d.String("version")][target]
	if pins.Archive == "" || pins.Binary == "" {
		return pins, errors.New("Untrusted connector version or platform: no pinned artifact")
	}
	return pins, nil
}

func VerifyIntegrity(data []byte, pin string) error {
	algorithm, want, _ := strings.Cut(pin, "-")
	actual := ""
	switch algorithm {
	case "sha256":
		v := sha256.Sum256(data)
		actual = hex.EncodeToString(v[:])
	case "sha512":
		v := sha512.Sum512(data)
		actual = base64.StdEncoding.EncodeToString(v[:])
	default:
		return errors.New("Unsupported artifact checksum")
	}
	if actual != want {
		return errors.New("Connector artifact checksum mismatch")
	}
	return nil
}

func DownloadVerified(ctx context.Context, urls []string, pin string) ([]byte, error) {
	var last error
	for _, url := range urls {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		data, err := func() ([]byte, error) {
			requestCtx, cancel := context.WithTimeout(ctx, 120*time.Second)
			defer cancel()
			req, e := http.NewRequestWithContext(requestCtx, http.MethodGet, url, nil)
			if e != nil {
				return nil, e
			}
			response, e := http.DefaultClient.Do(req)
			if e != nil {
				return nil, e
			}
			defer response.Body.Close()
			if response.StatusCode < 200 || response.StatusCode >= 300 {
				return nil, errors.New("Connector download unavailable")
			}
			bytes, e := io.ReadAll(io.LimitReader(response.Body, 128*1024*1024+1))
			if e != nil {
				return nil, e
			}
			if len(bytes) > 128*1024*1024 {
				return nil, errors.New("Connector download exceeds size limit")
			}
			return bytes, VerifyIntegrity(bytes, pin)
		}()
		if err == nil {
			return data, nil
		}
		last = err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if last == nil {
		last = errors.New("No connector download source")
	}
	return nil, last
}

type outputState struct {
	mu          sync.Mutex
	size, limit int
	failure     error
	cancel      context.CancelFunc
	onOutput    func(string)
}
type outputSink struct {
	state *outputState
	bytes *bytes.Buffer
}

func (w outputSink) Write(data []byte) (int, error) {
	w.state.mu.Lock()
	defer w.state.mu.Unlock()
	if w.state.failure != nil {
		return 0, w.state.failure
	}
	w.state.size += len(data)
	if w.state.size > w.state.limit {
		w.state.failure = errors.New("Connector output exceeds size limit")
		w.state.cancel()
		return 0, w.state.failure
	}
	n, e := w.bytes.Write(data)
	if w.state.onOutput != nil {
		w.state.onOutput(strings.ToValidUTF8(string(data), "�"))
	}
	return n, e
}

func runCaptured(ctx context.Context, command string, args []string, o RunOptions, limit int, base map[string]string) ([]byte, []byte, int, error) {
	if err := ctx.Err(); err != nil {
		return nil, nil, -1, err
	}
	if o.Timeout == 0 {
		o.Timeout = 30 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, o.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, command, args...)
	configureProcess(cmd)
	env := map[string]string{}
	for k, v := range base {
		env[k] = v
	}
	for k, v := range o.Environment {
		env[k] = v
	}
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	var stdout, stderr bytes.Buffer
	state := &outputState{limit: limit, cancel: cancel, onOutput: o.OnOutput}
	cmd.Stdout = outputSink{state, &stdout}
	cmd.Stderr = outputSink{state, &stderr}
	err := cmd.Run()
	if state.failure != nil {
		return nil, nil, -1, state.failure
	}
	if ctx.Err() != nil {
		return nil, nil, -1, ctx.Err()
	}
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			return nil, nil, -1, err
		}
		code = exit.ExitCode()
	}
	return stdout.Bytes(), stderr.Bytes(), code, nil
}

func managedInstallation(def protocol.JSONObject, root string, r Runtime) (Installation, error) {
	target := r.Platform + "-" + r.Arch
	var platforms []string
	_ = json.Unmarshal(def.Get("platforms"), &platforms)
	found := false
	for _, v := range platforms {
		found = found || v == target
	}
	if !found {
		return Installation{}, errors.New("Connector is unavailable for this platform")
	}
	if _, err := r.Pins(def, target); err != nil {
		return Installation{}, err
	}
	executable := map[string]string{"feishu": "lark-cli", "dingtalk": "dws", "wecom": "wecom-cli"}[def.String("id")]
	if executable == "" {
		return Installation{}, errors.New("Unknown managed native connector")
	}
	if r.Platform == "win32" {
		executable += ".exe"
	}
	dir := filepath.Join(root, "runtime", def.String("id"), def.String("version")+"-"+target)
	env := map[string]string{}
	if def.String("id") == "dingtalk" {
		home := filepath.Join(root, "accounts", def.String("id"))
		env = map[string]string{"DWS_CONFIG_DIR": filepath.Join(home, "dws"), "DWS_KEYCHAIN_DIR": filepath.Join(home, "keychain"), "DWS_DISABLE_KEYCHAIN": "1"}
	}
	order := []string{}
	if def.String("id") == "dingtalk" {
		order = []string{"DWS_CONFIG_DIR", "DWS_KEYCHAIN_DIR", "DWS_DISABLE_KEYCHAIN"}
	}
	return Installation{Directory: dir, Command: filepath.Join(dir, executable), Args: []string{}, Environment: env, EnvironmentOrder: order}, nil
}

var versionPieces = regexp.MustCompile(`[^0-9.]+`)

func prepareManaged(ctx context.Context, def protocol.JSONObject, root string, p Progress, r Runtime, uuid func() string) (Installation, error) {
	i, err := managedInstallation(def, root, r)
	if err != nil {
		return i, err
	}
	target := r.Platform + "-" + r.Arch
	pins, err := r.Pins(def, target)
	if err != nil {
		return i, err
	}
	probe := func(command string) error {
		data, e := os.ReadFile(command)
		if e != nil {
			return e
		}
		if e = VerifyIntegrity(data, pins.Binary); e != nil {
			return e
		}
		result, e := r.Run(ctx, command, []string{"--version"}, RunOptions{Environment: i.Environment})
		if e != nil {
			return e
		}
		match := false
		for _, v := range versionPieces.Split(result.Stdout, -1) {
			match = match || v == def.String("version")
		}
		if result.Code != 0 || !match {
			return errors.New("Connector runtime version check failed")
		}
		return nil
	}
	if probe(i.Command) == nil {
		return i, nil
	}
	if err = ctx.Err(); err != nil {
		return i, err
	}
	stage, backup := i.Directory+".stage-"+uuid(), i.Directory+".backup-"+uuid()
	if err = os.MkdirAll(stage, 0700); err != nil {
		return i, err
	}
	defer os.RemoveAll(stage)
	for _, key := range []string{"DWS_CONFIG_DIR", "DWS_KEYCHAIN_DIR"} {
		if path := i.Environment[key]; path != "" {
			if err = os.MkdirAll(path, 0700); err != nil {
				return i, err
			}
		}
	}
	p("downloading", nil)
	system, arch := "darwin", "arm64"
	if r.Platform == "win32" {
		system = "windows"
	}
	if r.Arch == "x64" {
		arch = "amd64"
	}
	suffix := ".tar.gz"
	if system == "windows" {
		suffix = ".zip"
	}
	executable := filepath.Base(i.Command)
	member := executable
	urls := []string{}
	if def.String("id") == "feishu" {
		asset := fmt.Sprintf("lark-cli-%s-%s-%s%s", def.String("version"), system, arch, suffix)
		urls = []string{"https://github.com/larksuite/cli/releases/download/v" + def.String("version") + "/" + asset, "https://registry.npmmirror.com/-/binary/lark-cli/v" + def.String("version") + "/" + asset}
	} else {
		path := "@wecom/cli-" + target + "/-/cli-" + target + "-" + def.String("version") + ".tgz"
		member = "package/bin/" + executable
		if def.String("id") == "dingtalk" {
			path = "dingtalk-workspace-cli/-/dingtalk-workspace-cli-" + def.String("version") + ".tgz"
			member = "package/assets/dws-" + system + "-" + arch + suffix
		}
		urls = []string{"https://registry.npmjs.org/" + path, "https://registry.npmmirror.com/" + path}
	}
	data, err := r.Download(ctx, urls, pins.Archive)
	if err != nil {
		return i, err
	}
	archive := filepath.Join(stage, "artifact")
	if err = os.WriteFile(archive, data, 0666); err != nil {
		return i, err
	}
	p("extracting", nil)
	binary, err := r.Extract(ctx, archive, member)
	if err != nil {
		return i, err
	}
	if def.String("id") == "dingtalk" {
		nested := filepath.Join(stage, "native-archive")
		if err = os.WriteFile(nested, binary, 0666); err != nil {
			return i, err
		}
		binary, err = r.Extract(ctx, nested, executable)
		if err != nil {
			return i, err
		}
		if err = os.Remove(nested); err != nil {
			return i, err
		}
	}
	staged := filepath.Join(stage, executable)
	if err = os.WriteFile(staged, binary, 0700); err != nil {
		return i, err
	}
	if err = os.Chmod(staged, 0700); err != nil {
		return i, err
	}
	if err = os.Remove(archive); err != nil {
		return i, err
	}
	p("verifying", nil)
	if err = probe(staged); err != nil {
		return i, err
	}
	if err = ctx.Err(); err != nil {
		return i, err
	}
	replaced := false
	if err = os.Rename(i.Directory, backup); err == nil {
		replaced = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return i, err
	}
	if err = os.Rename(stage, i.Directory); err != nil {
		if replaced {
			if restore := os.Rename(backup, i.Directory); restore != nil {
				return i, restore
			}
		}
		return i, err
	}
	if replaced {
		if err = os.RemoveAll(backup); err != nil {
			return i, err
		}
	}
	return i, nil
}
