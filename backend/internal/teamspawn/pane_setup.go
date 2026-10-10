package teamspawn

import "strings"

type PaneSetupChoice struct{ Value, Label, Description string }
type PaneSetupPrompt struct {
	Step, Title, Description string
	Choices                  []PaneSetupChoice
}
type PaneSetupResult struct {
	Success               bool   `json:"success"`
	Error                 string `json:"error,omitempty"`
	PackageManager        string `json:"packageManager,omitempty"`
	NeedsPythonAPIEnabled bool   `json:"needsPythonApiEnabled,omitempty"`
}

func (p *PaneRuntime) NeedsITermSetup() bool { p.mu.Lock(); defer p.mu.Unlock(); return p.needsSetup }
func (p *PaneRuntime) ResetBackendDetection() {
	p.mu.Lock()
	p.backend = ""
	p.needsSetup = false
	p.mu.Unlock()
}
func (p *PaneRuntime) IsTmuxAvailable() bool {
	_, _, code := p.run("tmux", []string{"-V"})
	return code == 0
}
func (p *PaneRuntime) DetectPythonPackageManager() string {
	for _, candidate := range []struct{ binary, manager string }{{"uv", "uvx"}, {"pipx", "pipx"}, {"pip", "pip"}, {"pip3", "pip"}} {
		_, _, code := p.run("which", []string{candidate.binary})
		if code == 0 {
			return candidate.manager
		}
	}
	return ""
}
func (p *PaneRuntime) InstallITerm(packageManager string) PaneSetupResult {
	command, args := "", []string{}
	switch packageManager {
	case "uvx":
		command, args = "uv", []string{"tool", "install", "it2"}
	case "pipx":
		command, args = "pipx", []string{"install", "it2"}
	case "pip":
		command, args = "pip", []string{"install", "--user", "it2"}
	default:
		return PaneSetupResult{Error: "No Python package manager found (uvx, pipx, or pip)"}
	}
	_, stderr, code := p.runAt(p.environment["HOME"], command, args)
	if code != 0 && packageManager == "pip" {
		_, stderr, code = p.runAt(p.environment["HOME"], "pip3", args)
	}
	result := PaneSetupResult{Success: code == 0, PackageManager: packageManager}
	if !result.Success {
		result.Error = strings.TrimSuffix(strings.TrimSuffix(stderr, "\n"), "\r")
		if result.Error == "" {
			result.Error = "Unknown installation error"
		}
	}
	return result
}
func (p *PaneRuntime) VerifyITerm() PaneSetupResult {
	_, _, code := p.run("which", []string{"it2"})
	if code != 0 {
		return PaneSetupResult{Error: "it2 CLI is not installed or not in PATH"}
	}
	_, stderr, code := p.run("it2", []string{"session", "list"})
	if code == 0 {
		return PaneSetupResult{Success: true}
	}
	stderr = strings.TrimSuffix(strings.TrimSuffix(stderr, "\n"), "\r")
	lower := strings.ToLower(stderr)
	for _, needle := range []string{"api", "python", "connection refused", "not enabled"} {
		if strings.Contains(lower, needle) {
			return PaneSetupResult{Error: "Python API not enabled in iTerm2 preferences", NeedsPythonAPIEnabled: true}
		}
	}
	if stderr == "" {
		stderr = "Failed to communicate with iTerm2"
	}
	return PaneSetupResult{Error: stderr}
}
