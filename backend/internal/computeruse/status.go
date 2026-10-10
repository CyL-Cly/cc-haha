package computeruse

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnosticsapi"
	"math"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

const MinimumMacOSVersion = "14.4"

var parseIntPrefix = regexp.MustCompile(`^[+-]?[0-9]+`)

func parseIntJS(text string) (float64, bool) {
	token := parseIntPrefix.FindString(trim(text))
	if token == "" {
		return 0, false
	}
	number, err := strconv.ParseFloat(token, 64)
	return number, err == nil && !math.IsInf(number, 0) && !math.IsNaN(number)
}
func KernelEligible(platform, release string) bool {
	if platform != "darwin" {
		return false
	}
	parts := strings.Split(release, ".")
	if len(parts) < 2 {
		return false
	}
	major, a := parseIntJS(parts[0])
	minor, b := parseIntJS(parts[1])
	return a && b && (major > 23 || (major == 23 && minor >= 4))
}

func IsVersionAtLeast(version, minimum string) bool {
	parse := func(text string) []float64 {
		result := []float64{}
		for _, part := range strings.Split(text, ".") {
			value, ok := parseIntJS(part)
			if !ok || value < 0 {
				value = 0
			}
			result = append(result, value)
		}
		return result
	}
	actual, floor := parse(version), parse(minimum)
	length := len(actual)
	if len(floor) > length {
		length = len(floor)
	}
	for i := 0; i < length; i++ {
		a, b := 0.0, 0.0
		if i < len(actual) {
			a = actual[i]
		}
		if i < len(floor) {
			b = floor[i]
		}
		if a != b {
			return a > b
		}
	}
	return true
}
func ResolveCapability(platform string, version *string, helperPresent, kernelEligible bool) object {
	supported, engine, available, nativeSupported, reason := false, "unsupported", false, false, any("unsupported_platform")
	if platform == "win32" {
		supported, engine = true, "windows-compat"
	} else if platform == "darwin" {
		if version == nil && !kernelEligible {
			reason = "system_version_unknown"
		} else if version != nil && !IsVersionAtLeast(*version, MinimumMacOSVersion) {
			reason = "os_too_old"
		} else {
			supported, engine, available, nativeSupported = true, "macos-native", helperPresent, true
			if helperPresent {
				reason = nil
			} else {
				reason = "helper_missing"
			}
		}
	}
	return object{"supported": encode(supported), "engine": encode(engine), "cuHelper": encode(map[string]any{"available": available, "supported": nativeSupported, "minimumMacosVersion": MinimumMacOSVersion, "reason": reason})}
}

type Permissions struct {
	Accessibility   *bool   `json:"accessibility"`
	ScreenRecording *bool   `json:"screenRecording"`
	Error           *string `json:"error"`
}

func ParsePermissionSnapshot(stdout string) Permissions {
	lines := strings.Split(stdout, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		var envelope struct {
			OK     bool            `json:"ok"`
			Result json.RawMessage `json:"result"`
		}
		if json.Unmarshal([]byte(trim(lines[i])), &envelope) != nil || !envelope.OK || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
			continue
		}
		var result Permissions
		if json.Unmarshal(envelope.Result, &result) == nil {
			result.Error = nil
			return result
		}
	}
	return Permissions{}
}
func ResolvePermissionCardResult(result CommandResult) map[string]any {
	if result.Code != 0 {
		reason := trim(result.Stderr)
		if reason == "" {
			reason = fmt.Sprintf("permission card exited with code %d", result.Code)
		}
		return map[string]any{"ok": false, "reason": reason, "accessibility": nil, "screenRecording": nil}
	}
	perms := ParsePermissionSnapshot(result.Stdout)
	if perms.Accessibility == nil && perms.ScreenRecording == nil {
		return map[string]any{"ok": false, "reason": "permission card returned no permission snapshot", "accessibility": nil, "screenRecording": nil}
	}
	return map[string]any{"ok": true, "accessibility": perms.Accessibility, "screenRecording": perms.ScreenRecording}
}
func (service *Service) Status(ctx context.Context) (object, error) {
	var version *string
	kernelEligible := false
	if service.options.Platform == "darwin" {
		if result, err := service.run(ctx, "/usr/bin/sw_vers", []string{"-productVersion"}, nil); err == nil && result.Code == 0 && trim(result.Stdout) != "" {
			text := trim(result.Stdout)
			version = &text
		}
		if result, err := service.run(ctx, "/usr/bin/uname", []string{"-r"}, nil); err == nil && result.Code == 0 {
			kernelEligible = KernelEligible("darwin", trim(result.Stdout))
		}
	}
	eligible := service.options.Platform == "darwin" && ((version != nil && IsVersionAtLeast(*version, MinimumMacOSVersion)) || (version == nil && kernelEligible))
	helperPresent := false
	if eligible {
		bin, err := service.ResolveLaunchableHelper(ctx)
		helperPresent = err == nil && bin != ""
	}
	capability := ResolveCapability(service.options.Platform, version, helperPresent, kernelEligible)
	if service.options.Platform == "win32" {
		return service.windowsStatus(ctx, capability)
	}
	perms := Permissions{}
	if helperPresent {
		raw, err := service.callOneShot(ctx, "check_permissions", object{})
		if err != nil {
			text := err.Error()
			perms.Error = &text
			_, _ = service.options.Diagnostics.Record(diagnosticsapi.Input{Type: "computer_use_permission_probe_failed", Severity: "error", Summary: text, Details: encode(map[string]string{"command": "check_permissions", "hint": "permissions stay unknown until this call succeeds"})})
		} else {
			_ = json.Unmarshal(raw, &perms)
		}
	}
	capability["platform"] = encode(service.options.Platform)
	capability["arch"] = encode(service.options.Arch)
	capability["systemVersion"] = encode(version)
	capability["python"] = encode(map[string]any{"installed": false, "version": nil, "path": nil, "source": nil, "error": nil})
	capability["venv"] = encode(map[string]any{"created": false, "path": filepath.Join(service.options.ConfigDir, ".runtime", "venv")})
	capability["dependencies"] = encode(map[string]any{"installed": false, "requirementsFound": false})
	capability["permissions"] = encode(perms)
	return capability, nil
}
func (service *Service) PermissionCard(ctx context.Context) map[string]any {
	if service.options.Platform != "darwin" {
		return map[string]any{"ok": false, "reason": "unsupported", "accessibility": nil, "screenRecording": nil}
	}
	bin, err := service.ResolveLaunchableHelper(ctx)
	if err != nil || bin == "" {
		return map[string]any{"ok": false, "reason": "helper-missing", "accessibility": nil, "screenRecording": nil}
	}
	result, err := service.run(ctx, bin, []string{"request-access"}, nil)
	if err != nil && result.Code == 0 {
		result.Code = -1
		result.Stderr = err.Error()
	}
	return ResolvePermissionCardResult(result)
}
