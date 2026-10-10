package requestcontext

import "strings"

// Source getModelMaxOutputTokens default, after its real canonical/override
// producer. Supported fixed IDs reuse the shared request catalog exactly.
func RestoreDefaultOutputTokens(canonical string) (float64, error) {
	if n, _, e := modelLimits(canonical); e == nil {
		return n, nil
	}
	if canonical == "claude-opus-5-5" || canonical == "claude-sonnet-5-5" {
		return 128000, nil
	}
	for _, p := range []string{"fable-5", "opus-5", "opus-4-8", "opus-4-7", "opus-4-6", "sonnet-5"} {
		if strings.Contains(canonical, p) {
			return 64000, nil
		}
	}
	for _, p := range []string{"sonnet-4-6", "opus-4-5", "sonnet-4", "haiku-4", "opus-4-1", "opus-4"} {
		if strings.Contains(canonical, p) {
			return 32000, nil
		}
	}
	for _, p := range []string{"claude-3-opus", "claude-3-haiku"} {
		if strings.Contains(canonical, p) {
			return 4096, nil
		}
	}
	for _, p := range []string{"claude-3-sonnet", "3-5-sonnet", "3-5-haiku"} {
		if strings.Contains(canonical, p) {
			return 8192, nil
		}
	}
	return 32000, nil
}
