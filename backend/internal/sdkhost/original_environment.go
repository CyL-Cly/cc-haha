package sdkhost

import "github.com/NanmiCoder/cc-haha/backend/internal/modelyield"

// originalRequestEnvironment derives the request view only after validating
// original host inputs. The complete map stays in Host.options.OriginalEnv for
// the Source consumers and evidence; these names are not SDK request options.
// This is a narrow bare/custom-system/empty-settings snapshot, not a wildcard
// environment allowlist, nor permission to activate the referenced features.
func originalRequestEnvironment(original map[string]string) (map[string]string, error) {
	return requestEnvironmentForProfile(original, false)
}
func requestEnvironmentForProfile(original map[string]string, neutral bool) (map[string]string, error) {
	out := map[string]string{}
	for key, value := range original {
		switch key {
		case "CLAUDE_CODE_ENTRYPOINT":
			if !neutral || value != "sdk" {
				return nil, cap("original environment CLAUDE_CODE_ENTRYPOINT")
			}
			out[key] = value
		case "ANTHROPIC_BASE_URL", "ANTHROPIC_API_KEY", "CLAUDE_CODE_SIMPLE", "CLAUDE_CODE_DISABLE_FAST_MODE", "CLAUDE_CODE_MAX_RETRIES", "NODE_EXTRA_CA_CERTS", "API_TIMEOUT_MS":
			out[key] = value
		case "CLAUDE_CODE_DIAGNOSTICS_FILE":
			// Retained by the original process registry and consumed only by the
			// borrowed synchronous sink; never an SDK request/header/body input.
		case "CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX", "CLAUDE_ENABLE_STREAM_WATCHDOG":
			// Separate producer policy consumer; never request/header/body inputs.
		case "CLAUDE_CODE_DISABLE_AUTO_MEMORY", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "CLAUDE_CODE_DISABLE_GIT_INSTRUCTIONS":
			if value != "1" {
				return nil, cap("original inactive host environment " + key)
			}
		case "CLAUDE_CODE_SKIP_REMOTE_PREFETCH":
			// There is no consumer of this exact name in the pinned release. Retain
			// the original field rather than changing it to LOCAL_SKIP_REMOTE_PREFETCH.
			if value != "1" {
				return nil, cap("original inert environment " + key)
			}
		case "CLAUDE_CODE_RESUME_INTERRUPTED_TURN":
			// The string "false" is truthy in Source. It is inactive only because this
			// admitted bare fresh host has no TurnInterruptionState; resume is separate.
			if value != "false" {
				return nil, cap("original interrupted-turn environment")
			}
		default:
			return nil, cap("original environment " + key)
		}
	}
	return out, nil
}

// Preserve presence and raw values. Only the actual partial-stream error branch
// needs an implemented recovery policy; ordinary success must remain reachable.
func producerPolicyFromOriginalEnvironment(original map[string]string, native ...bool) (*modelyield.StreamErrorPolicy, error) {
	policy := map[string]string{}
	for _, key := range []string{"CLAUDE_CODE_DISABLE_NONSTREAMING_FALLBACK", "CLAUDE_STREAM_TRANSIENT_RETRY_MAX", "CLAUDE_ENABLE_STREAM_WATCHDOG"} {
		if value, present := original[key]; present {
			policy[key] = value
		}
	}
	if len(native) > 0 && native[0] {
		return modelyield.PreserveNativeStreamErrorPolicyOriginalEnvironment(policy), nil
	}
	return modelyield.PreserveStreamErrorPolicyOriginalEnvironment(policy), nil
}
