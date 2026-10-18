package clibootstrap

import (
	"context"
	"errors"
	"path/filepath"
	"sync"

	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/openaiauth"
)

// Every consumer of one process boundary shares Source's two one-shot slots,
// including borrowed command bundles and a detached Agent's scoped boundary.
// Token-memo invalidation intentionally does not invalidate inherited pipes.
func (b *NativeOpenAIAuth) credentialFromDescriptor(env map[string]string, key, filename string) string {
	n := b.main.options.Native
	directory := n.CCRCredentialDirectory
	if directory == "" {
		directory = "/home/claude/.claude/remote"
	}
	return n.credentialFromDescriptor(env, b.main.options.Platform, directory, key, filename)
}

type nativeCredentialDescriptors struct {
	mu     sync.Mutex
	values map[string]string
}

func (n *NativeBoundary) credentialDescriptors() *nativeCredentialDescriptors {
	n.mu.Lock()
	defer n.mu.Unlock()
	if n.credentials == nil {
		n.credentials = &nativeCredentialDescriptors{values: map[string]string{}}
	}
	return n.credentials
}

func (n *NativeBoundary) credentialFromDescriptor(env map[string]string, platform, directory, key, filename string) string {
	cache := n.credentialDescriptors()
	cache.mu.Lock()
	defer cache.mu.Unlock()
	if value, loaded := cache.values[key]; loaded {
		return value
	}
	files := nativeOpenAIAuthFiles{n}
	value := desktopoauth.ReadCredentialDescriptor(desktopoauth.CredentialDescriptorOptions{
		Descriptor: env[key], Platform: platform,
		FallbackPath: filepath.Join(directory, filename), Remote: envTruthyNative(env["CLAUDE_CODE_REMOTE"]),
		ReadFile: n.readFile, MkdirAll: files.MkdirAll, WriteFile: files.WriteFile,
	})
	cache.values[key] = value
	return value
}

// Source getAnthropicApiKeyWithSource's value and category share one decision.
// Inspecting the category never runs a helper. The real client still awaits
// its existing helper producer before consulting that producer's sync cache.
func (b *NativeOpenAIAuth) apiKeyWithSource(ctx context.Context, env map[string]string, bare, preferThirdParty bool) (string, string, error) {
	key := env["ANTHROPIC_API_KEY"]
	if key != "" && (bare || preferThirdParty) {
		return key, "ANTHROPIC_API_KEY", nil
	}
	helper := func() (string, string, error) {
		command, err := b.main.nativeConfiguredAPIKeyHelper()
		if err != nil {
			return "", "", err
		}
		if command != "" {
			if b.main.apiKeyHelper != nil {
				if value := b.main.apiKeyHelper.Cached(); value != nil {
					return *value, "apiKeyHelper", nil
				}
			}
			return "", "apiKeyHelper", nil
		}
		return "", "none", nil
	}
	if bare {
		return helper()
	}
	readFD := func() string {
		return b.credentialFromDescriptor(env, "CLAUDE_CODE_API_KEY_FILE_DESCRIPTOR", ".api_key")
	}
	if envTruthyNative(env["CI"]) || env["NODE_ENV"] == "test" {
		if value := readFD(); value != "" {
			return value, "ANTHROPIC_API_KEY", nil
		}
		if key != "" {
			return key, "ANTHROPIC_API_KEY", nil
		}
		if env["CLAUDE_CODE_OAUTH_TOKEN"] == "" && env["CLAUDE_CODE_OAUTH_TOKEN_FILE_DESCRIPTOR"] == "" {
			return "", "", errors.New("ANTHROPIC_API_KEY or CLAUDE_CODE_OAUTH_TOKEN env var is required")
		}
		return "", "none", nil
	}
	if key != "" {
		global, err := b.main.Global.Get()
		if err != nil {
			return "", "", err
		}
		if global != nil {
			approved, err := openaiauth.IsCustomAPIKeyApproved(*global, quoteMain(key))
			if err != nil {
				return "", "", err
			}
			if approved {
				return key, "ANTHROPIC_API_KEY", nil
			}
		}
	}
	if value := readFD(); value != "" {
		return value, "ANTHROPIC_API_KEY", nil
	}
	if value, source, err := helper(); err != nil || source != "none" {
		return value, source, err
	}
	if !b.legacyAPIKeyLoaded {
		if err := b.ensureLegacyAPIKeyCached(ctx); err != nil {
			return "", "", err
		}
	}
	if b.legacyAPIKeyPresent {
		return b.legacyAPIKey, "/login managed key", nil
	}
	return "", "none", nil
}
