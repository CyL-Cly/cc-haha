package opentargets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

func (s *Service) bindPrimitives(p Primitives) Primitives {
	if p.ResolveCommand == nil {
		p.ResolveCommand = s.resolveCommand
	}
	if p.CommandExists == nil {
		p.CommandExists = func(ctx context.Context, c string) bool {
			path, err := p.ResolveCommand(ctx, c)
			return err == nil && path != ""
		}
	}
	if p.PathExists == nil {
		p.PathExists = func(path string) bool {
			stat, err := os.Stat(path)
			return err == nil && (stat.IsDir() || stat.Mode().IsRegular())
		}
	}
	if p.Launch == nil {
		p.Launch = s.launch
	}
	if p.ReadDirNames == nil {
		p.ReadDirNames = func(path string) []string {
			entries, err := os.ReadDir(path)
			result := []string{}
			if err == nil {
				for _, entry := range entries {
					result = append(result, entry.Name())
				}
			}
			return result
		}
	}
	if p.ReadTextFile == nil {
		p.ReadTextFile = func(path string) *string {
			data, err := os.ReadFile(path)
			if err != nil {
				return nil
			}
			text := string(protocol.DecodeJavaScriptUTF8(data))
			return &text
		}
	}
	if p.ReadPlistValue == nil {
		p.ReadPlistValue = s.readPlist
	}
	if p.ConvertIconToPNG == nil {
		p.ConvertIconToPNG = s.convertIcon
	}
	if p.ListApplicationsForFile == nil {
		p.ListApplicationsForFile = s.queryApplications
	}
	return p
}
func (s *Service) environment(extra map[string]string) []string {
	values := map[string]string{}
	for key, value := range s.options.Environment {
		values[key] = value
	}
	for key, value := range extra {
		values[key] = value
	}
	keys := []string{}
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]string, 0, len(keys))
	for _, key := range keys {
		result = append(result, key+"="+values[key])
	}
	return result
}
func (s *Service) binary(command string) (string, error) {
	if filepath.IsAbs(command) || strings.ContainsAny(command, "/\\") {
		return command, nil
	}
	pathEnv := s.options.Environment["PATH"]
	if pathEnv == "" {
		pathEnv = s.options.Environment["Path"]
	}
	extensions := []string{""}
	if runtime.GOOS == "windows" && filepath.Ext(command) == "" {
		extensions = strings.Split(s.options.Environment["PATHEXT"], ";")
		if len(extensions) == 1 && extensions[0] == "" {
			extensions = []string{".COM", ".EXE", ".BAT", ".CMD"}
		}
	}
	for _, dir := range filepath.SplitList(pathEnv) {
		if dir == "" {
			dir = s.options.CWD
		}
		for _, ext := range extensions {
			candidate := filepath.Join(dir, command+ext)
			stat, err := os.Stat(candidate)
			if err == nil && stat.Mode().IsRegular() && (runtime.GOOS == "windows" || stat.Mode().Perm()&0111 != 0) {
				return candidate, nil
			}
		}
	}
	return "", fmt.Errorf("executable not found in configured PATH: %s", command)
}

// Keep the buffer named: embedding bytes.Buffer would promote ReadFrom and
// allow os/exec's io.Copy to bypass the output cap in Write.
type cappedBuffer struct{ buffer bytes.Buffer }

func (b *cappedBuffer) String() string { return b.buffer.String() }

func (b *cappedBuffer) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 1024*1024 {
		return 0, errors.New("native command output exceeded 1 MiB")
	}
	return b.buffer.Write(p)
}
func (s *Service) run(ctx context.Context, command string, args []string, extra map[string]string, timeout time.Duration) (string, error) {
	binary, err := s.binary(command)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = s.options.CWD
	cmd.Env = s.environment(extra)
	configureProbe(cmd)
	var stdout, stderr cappedBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err = cmd.Run(); err != nil {
		return "", err
	}
	return stdout.String(), nil
}
func (s *Service) resolveCommand(ctx context.Context, command string) (string, error) {
	probe := "which"
	if s.options.Platform == "win32" {
		probe = "where"
	}
	text, err := s.run(ctx, probe, []string{command}, nil, 3*time.Second)
	if err != nil {
		return "", nil
	}
	for _, line := range strings.Split(text, "\n") {
		if line = trimJS(line); line != "" {
			return line, nil
		}
	}
	return "", nil
}
func (s *Service) launch(_ context.Context, command string, args []string) (LaunchResult, error) {
	binary, err := s.binary(command)
	if err != nil {
		return LaunchResult{Code: 1, Stderr: err.Error()}, nil
	}
	cmd := exec.Command(binary, args...)
	cmd.Dir = s.options.CWD
	cmd.Env = s.environment(nil)
	configureLaunch(cmd)
	if err = cmd.Start(); err != nil {
		return LaunchResult{Code: 1, Stderr: err.Error()}, nil
	}
	// Reap detached launchers without waiting for their GUI lifetime in HTTP.
	go func() { _ = cmd.Wait() }()
	return LaunchResult{}, nil
}
func (s *Service) readPlist(ctx context.Context, plist, key string) *string {
	output, err := s.run(ctx, "/usr/bin/plutil", []string{"-extract", key, "raw", "-o", "-", plist}, nil, 3*time.Second)
	if err != nil {
		return nil
	}
	value := trimJS(output)
	if value == "" {
		return nil
	}
	return &value
}
func (s *Service) queryApplications(ctx context.Context, target string) (ApplicationList, error) {
	if s.options.Platform != "darwin" {
		return ApplicationList{Applications: []Application{}}, nil
	}
	output, err := s.run(ctx, "/usr/bin/osascript", []string{"-l", "JavaScript", "-e", darwinApplicationQuery, target}, nil, 5*time.Second)
	if err != nil {
		return ApplicationList{Applications: []Application{}}, nil
	}
	return ParseApplicationList([]byte(output)), nil
}
func (s *Service) convertIcon(ctx context.Context, source string, size int) ([]byte, error) {
	ext := strings.ToLower(hostExt(source))
	if ext == ".png" {
		return os.ReadFile(source)
	}
	temp, err := os.MkdirTemp(s.options.TempDir, "cc-haha-open-target-icon-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temp)
	output := filepath.Join(temp, "icon.png")
	if s.options.Platform == "win32" {
		_, err = s.run(ctx, "powershell.exe", []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", windowsIconQuery}, map[string]string{"CC_HAHA_ICON_SOURCE": source, "CC_HAHA_ICON_OUTPUT": output}, 5*time.Second)
	} else if ext == ".svg" || ext == ".xpm" {
		attempts := []launchPlan{{"rsvg-convert", []string{"-w", strconv.Itoa(size), "-h", strconv.Itoa(size), "-o", output, source}}, {"gdk-pixbuf-thumbnailer", []string{"-s", strconv.Itoa(size), source, output}}, {"magick", []string{source, "-resize", fmt.Sprintf("%dx%d", size, size), output}}, {"convert", []string{source, "-resize", fmt.Sprintf("%dx%d", size, size), output}}}
		for _, attempt := range attempts {
			_, err = s.run(ctx, attempt.command, attempt.args, nil, 5*time.Second)
			if err == nil {
				break
			}
		}
	} else {
		_, err = s.run(ctx, "/usr/bin/sips", []string{"-z", strconv.Itoa(size), strconv.Itoa(size), "-s", "format", "png", source, "--out", output}, nil, 5*time.Second)
	}
	if err != nil {
		return nil, err
	}
	return os.ReadFile(output)
}

const windowsIconQuery = `
Add-Type -AssemblyName System.Drawing
$source = $env:CC_HAHA_ICON_SOURCE
$output = $env:CC_HAHA_ICON_OUTPUT
$icon = [System.Drawing.Icon]::ExtractAssociatedIcon($source)
if ($null -eq $icon) { exit 2 }
$bitmap = $icon.ToBitmap()
$bitmap.Save($output, [System.Drawing.Imaging.ImageFormat]::Png)
$bitmap.Dispose()
$icon.Dispose()
`

// This bridge only samples native LaunchServices/AppKit metadata. All target
// selection, authorization, ranking, dedupe and caches remain native Go.
const darwinApplicationQuery = `
ObjC.import('AppKit')
const args = $.NSProcessInfo.processInfo.arguments
const filePath = ObjC.unwrap(args.objectAtIndex(args.count - 1))
const fileURL = $.NSURL.fileURLWithPath(filePath)
const workspace = $.NSWorkspace.sharedWorkspace
const fileManager = $.NSFileManager.defaultManager
const defaultURL = workspace.URLForApplicationToOpenURL(fileURL)
const applicationURLs = workspace.URLsForApplicationsToOpenURL(fileURL)
const defaultApplicationPath = defaultURL ? ObjC.unwrap(defaultURL.path) : null
const applications = []
for (let index = 0; index < applicationURLs.count; index += 1) {
  const applicationURL = applicationURLs.objectAtIndex(index)
  const appPath = ObjC.unwrap(applicationURL.path)
  const bundle = $.NSBundle.bundleWithURL(applicationURL)
  const bundleId = bundle ? ObjC.unwrap(bundle.bundleIdentifier) : null
  const metadata = $.NSMetadataItem.alloc.initWithURL(applicationURL)
  const useCount = metadata ? ObjC.unwrap(metadata.valueForAttribute('kMDItemUseCount')) : null
  const canonicalURL = bundleId ? workspace.URLForApplicationWithBundleIdentifier(bundleId) : null
  applications.push({
    appPath, bundleId,
    displayName: ObjC.unwrap(fileManager.displayNameAtPath(appPath)),
    isDefault: appPath === defaultApplicationPath,
    useCount: typeof useCount === 'number' ? useCount : null,
    canonicalPath: canonicalURL ? ObjC.unwrap(canonicalURL.path) : null,
  })
}
JSON.stringify({ defaultApplicationPath, applications })
`
