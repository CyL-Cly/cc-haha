package clibootstrap

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

//go:embed native_chrome_prompt.md
var nativeChromePrompt string

const nativeChromeSkillHint = `**Browser Automation**: Chrome browser tools are available via the "claude-in-chrome" skill. CRITICAL: Before using any mcp__claude-in-chrome__* tools, invoke the skill by calling the Skill tool with skill: "claude-in-chrome". The skill provides browser automation instructions and enables the tools.`

type nativeChromeInstallation struct {
	done chan struct{}
	once sync.Once
}

func nativeChromeRequested(flag *bool, interactive bool, env map[string]string, global protocol.JSONObject) bool {
	if !interactive && (flag == nil || !*flag) {
		return false
	}
	if flag != nil {
		return *flag
	}
	if pickerTrue(env["CLAUDE_CODE_ENABLE_CFC"]) {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(env["CLAUDE_CODE_ENABLE_CFC"])) {
	case "0", "false", "no", "off":
		return false
	}
	return string(global.Get("claudeInChromeDefaultEnabled")) == "true"
}

func (m *MainPrint) initializeNativeChrome() error {
	if m.agentChild {
		return nil
	}
	return m.Owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		global, err := m.Global.Get()
		if err != nil {
			return err
		}
		config := protocol.NewJSONObject()
		if global != nil {
			config = global.Clone()
		}
		env := m.nativeOpenAIEnvironment()
		enabled := nativeChromeRequested(m.options.Arguments.Chrome, m.options.Interactive, env, config)
		if enabled {
			// main.tsx is an external build: its literal USER_TYPE bypass is
			// false. Use the same actual credential authority as the model.
			subscriber, err := m.openAIAuth.IsClaudeSubscriber(context.Background(), scope, !m.options.Interactive)
			if err != nil {
				return err
			}
			enabled = subscriber
		}
		// The frozen GrowthBook client key is empty. Cached remote features
		// cannot activate auto enable; Source's ant runtime branch still can.
		auto := !enabled && m.options.Interactive && string(config.Get("cachedChromeExtensionInstalled")) == "true" && env["USER_TYPE"] == "ant"
		if !enabled && m.options.Interactive && m.chromeCacheScan == nil {
			// Source returns the cache immediately and refreshes only positive
			// detections, so a machine without Chrome cannot poison shared config.
			m.chromeCacheScan = &nativeChromeInstallation{done: make(chan struct{})}
			job := m.chromeCacheScan
			native, platform := m.options.Native.agentScopedBoundary(m.options.Native.CWD), m.options.Platform
			go func() {
				defer close(job.done)
				if !nativeChromeExtensionInstalled(native, platform) {
					return
				}
				_ = m.Owner.Run(context.Background(), func(*cooperative.Scope) error {
					return m.Global.Save(func(current *protocol.JSONObject) (*protocol.JSONObject, error) {
						if current == nil {
							current = new(protocol.JSONObject)
							*current = protocol.NewJSONObject()
						}
						if string(current.Get("cachedChromeExtensionInstalled")) == "true" {
							return current, nil
						}
						next := current.Clone()
						next.Set("cachedChromeExtensionInstalled", []byte("true"))
						return &next, nil
					})
				})
			}()
		}
		if !enabled && !auto {
			return nil
		}
		binary, err := os.Executable()
		if err != nil {
			if auto {
				return nil
			}
			return fmt.Errorf("Error: Failed to run with Claude in Chrome: %w", err)
		}
		server := protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoteMain("stdio")}, protocol.JSONField{Name: "command", Value: quoteMain(binary)}, protocol.JSONField{Name: "args", Value: rawMain([]string{"--claude-in-chrome-mcp"})}, protocol.JSONField{Name: "scope", Value: quoteMain("dynamic")})
		if m.options.Arguments.DangerouslySkipPermissions || m.options.Arguments.PermissionMode == "bypassPermissions" {
			server.Set("env", rawMain(map[string]string{"CLAUDE_CHROME_PERMISSION_MODE": "skip_all_permission_checks"}))
		}
		m.nativeDynamicMCP.Set("claude-in-chrome", rawMain(server))
		appendix := m.options.Arguments.AppendSystemPrompt
		if enabled {
			if appendix != "" {
				appendix = nativeChromePrompt + "\n\n" + appendix
			} else {
				appendix = nativeChromePrompt
			}
		} else if appendix != "" {
			appendix += "\n\n" + nativeChromeSkillHint
		} else {
			appendix = nativeChromeSkillHint
		}
		m.options.Arguments.AppendSystemPrompt = appendix
		m.chromeInstallation = &nativeChromeInstallation{done: make(chan struct{})}
		installation := m.chromeInstallation
		native, platform := m.options.Native.agentScopedBoundary(m.options.Native.CWD), m.options.Platform
		go func() {
			defer close(installation.done)
			// Source's native manifest installation is best effort and does
			// not block the prompt or fail the CLI on a filesystem error.
			_ = installNativeChromeHost(native, platform, binary)
		}()
		return nil
	})
}

func chromeBrowserDirectories(home, platform string) []string {
	var paths []string
	switch platform {
	case "darwin", "macos":
		for _, path := range []string{"Google/Chrome", "BraveSoftware/Brave-Browser", "Arc/User Data", "Microsoft Edge", "Chromium", "Vivaldi", "com.operasoftware.Opera"} {
			paths = append(paths, filepath.Join(home, "Library", "Application Support", filepath.FromSlash(path)))
		}
	case "linux":
		for _, path := range []string{"google-chrome", "BraveSoftware/Brave-Browser", "microsoft-edge", "chromium", "vivaldi", "opera"} {
			paths = append(paths, filepath.Join(home, ".config", filepath.FromSlash(path)))
		}
	case "windows", "win32":
		for _, path := range []string{"Google/Chrome/User Data", "BraveSoftware/Brave-Browser/User Data", "Arc/User Data", "Microsoft/Edge/User Data", "Chromium/User Data", "Vivaldi/User Data"} {
			paths = append(paths, filepath.Join(home, "AppData", "Local", filepath.FromSlash(path)))
		}
		paths = append(paths, filepath.Join(home, "AppData", "Roaming", "Opera Software", "Opera Stable"))
	}
	return paths
}

func nativeChromeExtensionInstalled(n *NativeBoundary, platform string) bool {
	ids := []string{"fcoeoabgfenejglbffodgkkbkcdhcgfn"}
	if n.Environment["USER_TYPE"] == "ant" {
		ids = append(ids, "dihbgbndebgnbjfmelmegjepbnkhlgni", "dngcpimnedloihjnnfngkgjoidhnaolf")
	}
	for _, base := range chromeBrowserDirectories(n.Home, platform) {
		profiles, err := n.readDir(base)
		if err != nil {
			continue
		}
		for _, profile := range profiles {
			if !profile.IsDir() || profile.Name() != "Default" && !strings.HasPrefix(profile.Name(), "Profile ") {
				continue
			}
			for _, id := range ids {
				if _, err := n.readDir(filepath.Join(base, profile.Name(), "Extensions", id)); err == nil {
					return true
				}
			}
		}
	}
	return false
}

func writeNativeChromeFile(n *NativeBoundary, path string, content []byte, executable bool) (bool, error) {
	if existing, err := n.readFile(path); err == nil && string(existing) == string(content) {
		return false, nil
	}
	if err := n.check("mkdirAll", filepath.Dir(path)); err != nil {
		return false, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return false, err
	}
	if err := n.check("writeFile", path); err != nil {
		return false, err
	}
	if err := os.WriteFile(path, content, 0644); err != nil {
		return false, err
	}
	if executable {
		return true, os.Chmod(path, 0755)
	}
	return true, nil
}

func installNativeChromeHost(n *NativeBoundary, platform, binary string) error {
	config := n.ConfigHomeDirectory()
	if config == "" {
		config = filepath.Join(n.Home, ".claude")
	} else if !filepath.IsAbs(config) {
		config = filepath.Join(n.CWD, config)
	}
	windows := platform == "windows" || platform == "win32"
	wrapper := filepath.Join(config, "chrome", "chrome-native-host")
	body := "#!/bin/sh\n# Chrome native host wrapper script\n# Generated by Claude Code - do not edit manually\nexec " + "'" + strings.ReplaceAll(binary, "'", "'\\''") + "' --chrome-native-host\n"
	if windows {
		wrapper += ".bat"
		body = "@echo off\nREM Chrome native host wrapper script\nREM Generated by Claude Code - do not edit manually\n\"" + binary + "\" --chrome-native-host\n"
	}
	if _, err := writeNativeChromeFile(n, wrapper, []byte(body), !windows); err != nil {
		return err
	}
	origins := []string{"chrome-extension://fcoeoabgfenejglbffodgkkbkcdhcgfn/"}
	if n.Environment["USER_TYPE"] == "ant" {
		origins = append(origins, "chrome-extension://dihbgbndebgnbjfmelmegjepbnkhlgni/", "chrome-extension://dngcpimnedloihjnnfngkgjoidhnaolf/")
	}
	manifest := protocol.NewJSONObject(protocol.JSONField{Name: "name", Value: quoteMain("com.anthropic.claude_code_browser_extension")}, protocol.JSONField{Name: "description", Value: quoteMain("Claude Code Browser Extension Native Host")}, protocol.JSONField{Name: "path", Value: quoteMain(wrapper)}, protocol.JSONField{Name: "type", Value: quoteMain("stdio")}, protocol.JSONField{Name: "allowed_origins", Value: rawMain(origins)})
	content, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	directories := chromeBrowserDirectories(n.Home, platform)
	if windows {
		appdata := n.Environment["APPDATA"]
		if appdata == "" {
			appdata = filepath.Join(n.Home, "AppData", "Local")
		}
		directories = []string{filepath.Join(appdata, "Claude Code", "ChromeNativeHost")}
	} else {
		for i := range directories {
			directories[i] = filepath.Join(directories[i], "NativeMessagingHosts")
		}
	}
	updated := false
	for _, directory := range directories {
		changed, err := writeNativeChromeFile(n, filepath.Join(directory, "com.anthropic.claude_code_browser_extension.json"), content, false)
		updated = updated || changed && err == nil
	}
	if windows && len(directories) > 0 {
		manifestPath := filepath.Join(directories[0], "com.anthropic.claude_code_browser_extension.json")
		for _, key := range []string{"Google\\Chrome", "BraveSoftware\\Brave-Browser", "ArcBrowser\\Arc", "Microsoft\\Edge", "Chromium", "Vivaldi", "Opera Software\\Opera Stable"} {
			command := exec.Command("reg", "add", "HKCU\\Software\\"+key+"\\NativeMessagingHosts\\com.anthropic.claude_code_browser_extension", "/ve", "/t", "REG_SZ", "/d", manifestPath, "/f")
			command.Env = chromeCommandEnvironment(n.Environment)
			_ = command.Run()
		}
	}
	if updated && nativeChromeExtensionInstalled(n, platform) {
		openNativeChromeURL(n, platform, "https://clau.de/chrome/reconnect")
	}
	return nil
}

func chromeCommandEnvironment(env map[string]string) []string {
	var result []string
	for key, value := range env {
		result = append(result, key+"="+value)
	}
	return result
}

func (m *MainPrint) joinNativeChromeInstallation() {
	for _, job := range []*nativeChromeInstallation{m.chromeCacheScan, m.chromeInstallation} {
		if job != nil {
			job.once.Do(func() { <-job.done })
		}
	}
}

func openNativeChromeURL(n *NativeBoundary, platform, address string) {
	names := []string{"Google Chrome", "Brave Browser", "Arc", "Microsoft Edge", "Chromium", "Vivaldi", "Opera"}
	binaries := [][]string{{"google-chrome", "google-chrome-stable"}, {"brave-browser", "brave"}, {}, {"microsoft-edge", "microsoft-edge-stable"}, {"chromium", "chromium-browser"}, {"vivaldi", "vivaldi-stable"}, {"opera"}}
	run := func(command string, args ...string) bool {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		child := exec.CommandContext(ctx, command, args...)
		child.Env = chromeCommandEnvironment(n.Environment)
		return child.Run() == nil
	}
	for i, name := range names {
		switch platform {
		case "darwin", "macos":
			if info, err := n.stat(filepath.Join("/Applications", name+".app")); err == nil && info.IsDir() {
				_ = run("open", "-a", name, address)
				return
			}
		case "linux":
			available := false
			for _, binary := range binaries[i] {
				for _, directory := range filepath.SplitList(n.Environment["PATH"]) {
					if !filepath.IsAbs(directory) {
						directory = filepath.Join(n.CWD, directory)
					}
					if info, err := n.stat(filepath.Join(directory, binary)); err == nil && !info.IsDir() && info.Mode()&0111 != 0 {
						available = true
						break
					}
				}
			}
			if available {
				for _, binary := range binaries[i] {
					if run(binary, address) {
						return
					}
				}
				return
			}
		case "windows", "win32":
			paths := chromeBrowserDirectories(n.Home, platform)
			if i < len(paths) {
				if info, err := n.stat(paths[i]); err == nil && info.IsDir() {
					_ = run("rundll32", "url,OpenURL", address)
					return
				}
			}
		}
	}
}
