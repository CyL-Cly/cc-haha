package requestcontext

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/toolsearch"
	weburl "github.com/nlnwa/whatwg-url/url"
)

func nativeCustomBetaRelay(env map[string]string) bool {
	if toolsearch.Provider(env) != "firstParty" {
		return false
	}
	value := env["ANTHROPIC_BASE_URL"]
	if value == "" {
		return false
	}
	parsed, err := weburl.Parse(value)
	return err != nil || parsed.Host() != "api.anthropic.com" && !(env["USER_TYPE"] == "ant" && parsed.Host() == "api-staging.anthropic.com")
}

// MergeSDKBetas follows getMergedBetas: custom relays use Set uniqueness; other
// providers filter SDK values against the original base, preserving repeated
// SDK opt-ins when that beta was not already in the base.
func MergeSDKBetas(base, sdk []string, customRelay bool) []string {
	result := append([]string{}, base...)
	if customRelay {
		result = []string{}
		for _, value := range base {
			result = appendUnique(result, value)
		}
	}
	for _, value := range sdk {
		if customRelay {
			result = appendUnique(result, value)
			continue
		}
		found := false
		for _, existing := range base {
			if existing == value {
				found = true
				break
			}
		}
		if !found {
			result = append(result, value)
		}
	}
	return result
}
