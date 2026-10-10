package computeruse

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

const helperAppName = "cc-haha-computer-use.app"
const helperInner = "Contents/MacOS/cc-haha-computer-use"
const helperIdentifier = "dev.cchaha.cu-helper"
const sidecarIdentifier = "com.claude-code-haha.desktop.sidecar"

type commandBuffer struct{ buffer bytes.Buffer }

func (buffer *commandBuffer) String() string { return buffer.buffer.String() }
func (buffer *commandBuffer) Write(data []byte) (int, error) {
	if buffer.buffer.Len()+len(data) > 64*1024*1024 {
		return 0, errors.New("Computer Use native command output exceeded 64 MiB")
	}
	return buffer.buffer.Write(data)
}
func nativeRunner(options Options) Runner {
	env := make([]string, 0, len(options.Environment))
	keys := []string{}
	for key := range options.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		env = append(env, key+"="+options.Environment[key])
	}
	return func(ctx context.Context, file string, args []string, extra []*os.File) (CommandResult, error) {
		if !filepath.IsAbs(file) && !strings.ContainsAny(file, "/\\") {
			found := ""
			for _, directory := range filepath.SplitList(options.Environment["PATH"]) {
				if directory == "" {
					directory = options.WorkDir
				}
				names := []string{file}
				if options.Platform == "win32" && filepath.Ext(file) == "" {
					extensions := options.Environment["PATHEXT"]
					if extensions == "" {
						extensions = ".COM;.EXE;.BAT;.CMD"
					}
					for _, ext := range strings.Split(extensions, ";") {
						if ext != "" {
							names = append(names, file+strings.ToLower(ext), file+strings.ToUpper(ext))
						}
					}
				}
				for _, name := range names {
					candidate := filepath.Join(directory, name)
					if info, err := os.Stat(candidate); err == nil && info.Mode().IsRegular() && (options.Platform == "win32" || info.Mode().Perm()&0111 != 0) {
						found = candidate
						break
					}
				}
				if found != "" {
					break
				}
			}
			if found == "" {
				return CommandResult{Code: -1}, fmt.Errorf("Failed to run %s", file)
			}
			file = found
		}
		command := exec.CommandContext(ctx, file, args...)
		configureNativeProcess(command)
		command.Env = env
		if tag, ok := ctx.Value(windowsInputTagKey{}).(string); ok {
			command.Env = append([]string{}, env...)
			for i, value := range command.Env {
				if strings.HasPrefix(value, "CC_HAHA_COMPUTER_USE_INPUT_TAG=") {
					command.Env = append(command.Env[:i], command.Env[i+1:]...)
					break
				}
			}
			command.Env = append(command.Env, "CC_HAHA_COMPUTER_USE_INPUT_TAG="+tag)
		}
		command.Dir = options.WorkDir
		command.ExtraFiles = extra
		command.WaitDelay = time.Second
		var stdout, stderr commandBuffer
		command.Stdout = &stdout
		command.Stderr = &stderr
		err := command.Run()
		result := CommandResult{Stdout: stdout.String(), Stderr: stderr.String()}
		if err != nil {
			result.Code = -1
			var exit *exec.ExitError
			if errors.As(err, &exit) {
				result.Code = exit.ExitCode()
			}
		}
		return result, err
	}
}
func (service *Service) run(ctx context.Context, file string, args []string, extra []*os.File) (CommandResult, error) {
	ctx, cancel := service.ownedContext(ctx)
	defer cancel()
	return service.options.Run(ctx, file, args, extra)
}
func exists(path string) bool { _, err := os.Stat(path); return err == nil }
func helperBundle(binary string) string {
	marker := string(filepath.Separator) + helperAppName + string(filepath.Separator)
	if index := strings.Index(binary, marker); index >= 0 {
		return binary[:index+1+len(helperAppName)]
	}
	return ""
}
func IsNestedInHostApp(bundle string) bool {
	for directory := filepath.Dir(bundle); directory != filepath.Dir(directory); directory = filepath.Dir(directory) {
		if strings.HasSuffix(strings.ToLower(directory), ".app") {
			return true
		}
	}
	return false
}

var asarPath = regexp.MustCompile(`\.asar($|[/\\])`)

func DevelopmentHelperBinary(projectRoot, arch string) string {
	swiftArch := ""
	if arch == "arm64" {
		swiftArch = "arm64"
	} else if arch == "x64" || arch == "amd64" {
		swiftArch = "x86_64"
	}
	if swiftArch == "" || projectRoot == "" {
		return ""
	}
	return filepath.Join(projectRoot, "native", "cu-helper", ".build", swiftArch, swiftArch+"-apple-macosx", "release", helperAppName, filepath.FromSlash(helperInner))
}
func (service *Service) ResolveSourceHelper() string {
	service.mu.Lock()
	defer service.mu.Unlock()
	if service.sourceResolved {
		return service.sourceBinary
	}
	service.sourceResolved = true
	service.sourceBinary = service.resolveSourceHelperUncached()
	return service.sourceBinary
}
func (service *Service) resolveSourceHelperUncached() string {
	packaged := strings.Contains(service.options.AppRoot, ".asar")
	if !packaged {
		if override := service.options.Environment["CC_HAHA_CU_HELPER_PATH"]; override != "" && exists(override) {
			return override
		}
		if development := DevelopmentHelperBinary(service.options.ProjectRoot, service.options.Arch); development != "" && exists(development) {
			return development
		}
	}
	if service.options.AppRoot != "" {
		root := service.options.AppRoot
		if location := asarPath.FindStringSubmatchIndex(root); location != nil {
			suffix := ""
			if location[2] >= 0 {
				suffix = root[location[2]:location[3]]
			}
			root = root[:location[0]] + ".asar.unpacked" + suffix + root[location[1]:]
		}
		candidate := filepath.Join(root, "src-tauri", "binaries", helperAppName, filepath.FromSlash(helperInner))
		if exists(candidate) {
			return candidate
		}
	}
	return ""
}
func (service *Service) ResolveLaunchableHelper(ctx context.Context) (string, error) {
	if service.options.Platform != "darwin" {
		return "", nil
	}
	// The synchronous kernel floor remains necessary when sw_vers cannot report.
	result, err := service.run(ctx, "/usr/bin/uname", []string{"-r"}, nil)
	if err != nil || result.Code != 0 || !KernelEligible("darwin", trim(result.Stdout)) {
		return "", nil
	}
	source := service.ResolveSourceHelper()
	if source == "" {
		return "", nil
	}
	bundle := helperBundle(source)
	if bundle == "" || !IsNestedInHostApp(bundle) {
		return source, nil
	}
	installed, err := service.installHelper(ctx, bundle)
	if err != nil {
		return "", err
	}
	return installed, nil
}
func bundleFingerprint(bundle string) (string, error) {
	hash := sha256.New()
	for _, relative := range []string{helperInner, "Contents/Info.plist", "Contents/_CodeSignature/CodeResources"} {
		data, err := os.ReadFile(filepath.Join(bundle, filepath.FromSlash(relative)))
		if err != nil {
			return "", err
		}
		_, _ = hash.Write([]byte(relative))
		_, _ = hash.Write([]byte{0})
		_, _ = hash.Write(data)
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

type codeIdentity struct{ identifier, authority, team, leaf string }

func (service *Service) codeIdentity(ctx context.Context, target string) (codeIdentity, error) {
	result, err := service.run(ctx, "/usr/bin/codesign", []string{"-dv", "--verbose=4", target}, nil)
	if err != nil || result.Code != 0 {
		return codeIdentity{}, errors.New("cu-helper signature identity probe failed")
	}
	identity := codeIdentity{}
	for _, line := range strings.Split(result.Stdout+result.Stderr, "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		value = trim(value)
		switch key {
		case "Identifier":
			identity.identifier = value
		case "Authority":
			if identity.authority == "" {
				identity.authority = value
			}
		case "TeamIdentifier":
			if value != "not set" {
				identity.team = value
			}
		}
	}
	temporary, err := os.MkdirTemp(service.options.TempDir, "cc-haha-cu-signature-")
	if err != nil {
		return identity, err
	}
	defer os.RemoveAll(temporary)
	prefix := filepath.Join(temporary, "cert-")
	result, err = service.run(ctx, "/usr/bin/codesign", []string{"-d", "--extract-certificates=" + prefix, target}, nil)
	if err != nil || result.Code != 0 {
		return identity, errors.New("cu-helper leaf certificate extraction failed")
	}
	leaf, err := os.ReadFile(prefix + "0")
	if err != nil {
		return identity, err
	}
	hash := sha256.Sum256(leaf)
	identity.leaf = hex.EncodeToString(hash[:])
	return identity, nil
}
func sameSigner(a, b codeIdentity) bool {
	return a.authority != "" && a.team != "" && a.leaf != "" && a.authority == b.authority && a.team == b.team && a.leaf == b.leaf
}
func (service *Service) verifyPackagedSignatures(ctx context.Context, source, destination string) bool {
	for _, spec := range [][]string{{"--verify", "--deep", "--strict", "--verbose=2", source}, {"--verify", "--deep", "--strict", "--verbose=2", destination}, {"--verify", "--strict", "--verbose=2", service.options.Executable}} {
		result, err := service.run(ctx, "/usr/bin/codesign", spec, nil)
		if err != nil || result.Code != 0 {
			return false
		}
	}
	a, err := service.codeIdentity(ctx, source)
	if err != nil {
		return false
	}
	b, err := service.codeIdentity(ctx, destination)
	if err != nil {
		return false
	}
	sidecar, err := service.codeIdentity(ctx, service.options.Executable)
	if err != nil {
		return false
	}
	return a.identifier == helperIdentifier && b.identifier == helperIdentifier && sidecar.identifier == sidecarIdentifier && sameSigner(a, b) && sameSigner(a, sidecar)
}
func withInstallLock(ctx context.Context, path string, operation func() error) error {
	deadline := time.Now().Add(5 * time.Second)
	token := fmt.Sprintf("%d:%d:%s", os.Getpid(), time.Now().UnixMilli(), randomID())
	var descriptor *os.File
	for descriptor == nil {
		var err error
		descriptor, err = os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err == nil {
			if _, err = io.WriteString(descriptor, token+"\n"); err != nil {
				_ = descriptor.Close()
				_ = os.Remove(path)
				return err
			}
			break
		}
		if !errors.Is(err, os.ErrExist) {
			return err
		}
		if info, err := os.Stat(path); err == nil && time.Since(info.ModTime()) > 30*time.Second {
			stale := path + ".stale-" + randomID()
			if os.Rename(path, stale) == nil {
				_ = os.Remove(stale)
			}
		}
		if !time.Now().Before(deadline) {
			return errors.New("timed out waiting for the cu-helper install lock")
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
	defer func() {
		_ = descriptor.Close()
		if raw, err := os.ReadFile(path); err == nil && trim(string(raw)) == token {
			_ = os.Remove(path)
		}
	}()
	return operation()
}
func (service *Service) installHelper(ctx context.Context, source string) (string, error) {
	root := filepath.Join(service.options.ConfigDir, "cu-helper")
	destination := filepath.Join(root, helperAppName)
	binary := filepath.Join(destination, filepath.FromSlash(helperInner))
	marker := filepath.Join(root, ".installed-version")
	fingerprint, err := bundleFingerprint(source)
	if err != nil {
		return "", err
	}
	verify := func() bool {
		raw, err := os.ReadFile(marker)
		if err != nil || trim(string(raw)) != fingerprint || !exists(binary) {
			return false
		}
		sum, err := bundleFingerprint(destination)
		return err == nil && sum == fingerprint && service.verifyPackagedSignatures(ctx, source, destination)
	}
	if verify() {
		return binary, nil
	}
	if err = os.MkdirAll(root, 0700); err != nil {
		return "", err
	}
	err = withInstallLock(ctx, filepath.Join(root, ".install.lock"), func() error {
		if verify() {
			return nil
		}
		staging := filepath.Join(root, "."+helperAppName+".staging-"+randomID())
		defer os.RemoveAll(staging)
		result, err := service.run(ctx, "/usr/bin/ditto", []string{"--noqtn", source, staging}, nil)
		if err != nil || result.Code != 0 {
			return errors.New("ditto --noqtn failed copying cu-helper")
		}
		copied, err := bundleFingerprint(staging)
		if err != nil || copied != fingerprint {
			return errors.New("copied cu-helper bundle fingerprint does not match source")
		}
		if !service.verifyPackagedSignatures(ctx, source, staging) {
			return errors.New("copied cu-helper signature does not match the packaged sidecar")
		}
		backup := destination + ".previous-" + randomID()
		hadDestination := exists(destination)
		if hadDestination {
			if err = os.Rename(destination, backup); err != nil {
				return err
			}
		}
		if err = os.Rename(staging, destination); err != nil {
			if hadDestination {
				_ = os.Rename(backup, destination)
			}
			return err
		}
		installed, err := bundleFingerprint(destination)
		if err != nil || installed != fingerprint || !service.verifyPackagedSignatures(ctx, source, destination) {
			removeErr := os.RemoveAll(destination)
			var rollbackErr error
			if hadDestination {
				rollbackErr = os.Rename(backup, destination)
			}
			return errors.Join(errors.New("installed cu-helper failed post-replacement verification"), removeErr, rollbackErr)
		}
		if hadDestination {
			if err = os.RemoveAll(backup); err != nil {
				return err
			}
		}
		return os.WriteFile(marker, []byte(fingerprint+"\n"), 0600)
	})
	if err != nil || !verify() {
		return "", errors.Join(err, errors.New("cu-helper standalone installation failed; refusing nested helper"))
	}
	return binary, nil
}

type NativeCommandError struct{ Message, Code string }

func (err *NativeCommandError) Error() string { return err.Message }
func parseEnvelope(command string, result CommandResult) (json.RawMessage, error) {
	if result.Code != 0 && trim(result.Stdout) == "" {
		message := trim(result.Stderr)
		if message == "" {
			message = fmt.Sprintf("cu-helper %s failed with code %d", command, result.Code)
		}
		return nil, errors.New(message)
	}
	var envelope struct {
		OK     bool                           `json:"ok"`
		Result json.RawMessage                `json:"result"`
		Error  struct{ Message, Code string } `json:"error"`
	}
	if json.Unmarshal([]byte(result.Stdout), &envelope) != nil {
		message := result.Stderr
		if message == "" {
			message = result.Stdout
		}
		if message == "" {
			message = "cu-helper " + command + " returned invalid JSON"
		}
		return nil, errors.New(message)
	}
	if !envelope.OK {
		if envelope.Error.Message == "" {
			envelope.Error.Message = "cu-helper " + command + " failed"
		}
		return nil, &NativeCommandError{envelope.Error.Message, envelope.Error.Code}
	}
	if len(envelope.Result) == 0 {
		envelope.Result = encode(nil)
	}
	return envelope.Result, nil
}
func (service *Service) callOneShot(ctx context.Context, command string, payload object) (json.RawMessage, error) {
	// The production helper denies other one-shot verbs; mirror that boundary.
	if command != "check_permissions" && command != "attest_daemon_peer" {
		return nil, errors.New("native_daemon_required: " + command + " requires the persistent macOS helper daemon")
	}
	binary, err := service.ResolveLaunchableHelper(ctx)
	if err != nil {
		return nil, err
	}
	if binary == "" {
		return nil, errors.New("cu-helper standalone installation failed or binary was not found. Build it: native/cu-helper/build.sh")
	}
	result, err := service.run(ctx, binary, []string{command, "--payload", string(encode(payload))}, nil)
	if err != nil && result.Stdout == "" && result.Stderr == "" {
		return nil, err
	}
	return parseEnvelope(command, result)
}
