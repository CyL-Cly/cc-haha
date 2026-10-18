package tui

import (
	"fmt"
	"path/filepath"
	"strings"
)

type ExportMemoryNotice struct {
	Path       string
	Characters int
}
type ExportStatusContext struct {
	CWD                                string
	Memory                             []ExportMemoryNotice
	AgentDescriptionTokens             float64
	Subscriber, StoredAPIKey           bool
	AuthTokenSource, APIKeySource      string
	JetBrainsIDE                       string
	AutoInstallIDE, JetBrainsInstalled bool
}

// ExportStatusNoticeLines uses the original six predicates. A static export
// passes no agentDefinitions, so its AgentDescriptionTokens producer is zero.
func ExportStatusNoticeLines(value ExportStatusContext, width int) []string {
	lines := []string{}
	warning := func(text string, margin bool) {
		if margin {
			lines = append(lines, "")
		}
		// Original Text measure first wraps at the row's available width. Its
		// longest measured line is the flex basis; a full-width text shrinks
		// the warning's relative offset to zero and paints over that glyph.
		available := max(1, width-1)
		measured := exportWrapWords(text, available)
		bodyWidth := 0
		for _, line := range measured {
			bodyWidth = max(bodyWidth, exportWidth(line))
		}
		shift := 1
		if bodyWidth >= available {
			shift = 0
		}
		for i, line := range exportWrapWords(text, max(1, bodyWidth)) {
			prefix := " " + strings.Repeat(" ", shift)
			if i == 0 && shift == 1 {
				prefix = " ⚠"
			}
			lines = append(lines, strings.TrimRight(prefix+line, " "))
		}
	}
	for _, file := range value.Memory {
		if file.Characters > 40000 {
			path := file.Path
			if strings.HasPrefix(path, value.CWD) {
				if relative, err := filepath.Rel(value.CWD, path); err == nil {
					path = relative
				}
			}
			warning(fmt.Sprintf("Large %s will impact performance (%s chars > %s) · /memory to edit", path, visualFormat(float64(file.Characters)), visualFormat(40000)), false)
		}
	}
	if value.AgentDescriptionTokens > 15000 {
		warning(fmt.Sprintf("Large cumulative agent descriptions will impact performance (~%s tokens > %s) · /agents to manage", visualFormat(value.AgentDescriptionTokens), visualFormat(15000)), false)
	}
	if value.Subscriber && (value.AuthTokenSource == "ANTHROPIC_AUTH_TOKEN" || value.AuthTokenSource == "apiKeyHelper") {
		warning("Auth conflict: Using "+value.AuthTokenSource+" instead of Claude account subscription token. Either unset "+value.AuthTokenSource+", or run `claude /logout`.", true)
	}
	if value.StoredAPIKey && (value.APIKeySource == "ANTHROPIC_API_KEY" || value.APIKeySource == "apiKeyHelper") {
		warning("Auth conflict: Using "+value.APIKeySource+" instead of Anthropic Console key. Either unset "+value.APIKeySource+", or run `claude /logout`.", true)
	}
	if value.APIKeySource != "none" && value.APIKeySource != "" && value.AuthTokenSource != "none" && value.AuthTokenSource != "" && !(value.APIKeySource == "apiKeyHelper" && value.AuthTokenSource == "apiKeyHelper") {
		warning("Auth conflict: Both a token ("+value.AuthTokenSource+") and an API key ("+value.APIKeySource+") are set. This may lead to unexpected behavior.", true)
		keyAdvice := "claude /logout"
		if value.APIKeySource == "ANTHROPIC_API_KEY" {
			keyAdvice = "Unset the ANTHROPIC_API_KEY environment variable, or claude /logout then say \"No\" to the API key approval before login."
		}
		if value.APIKeySource == "apiKeyHelper" {
			keyAdvice = "Unset the apiKeyHelper setting."
		}
		tokenAdvice := "Unset the " + value.AuthTokenSource + " environment variable."
		if value.AuthTokenSource == "claude.ai" {
			tokenAdvice = "claude /logout to sign out of claude.ai."
		}
		advice := []string{"· Trying to use " + value.AuthTokenSource + "? " + keyAdvice, "· Trying to use " + value.APIKeySource + "? " + tokenAdvice}
		first := exportWrapWords(advice[0], max(1, width-4))
		// The original column positions its second Text from the intrinsic
		// height before the final margin-constrained wrap; later paint wins.
		firstHeight := len(exportWrapWords(advice[0], max(1, width-1)))
		for _, line := range first[:min(len(first), firstHeight)] {
			lines = append(lines, strings.TrimRight("    "+line, " "))
		}
		for _, line := range exportWrapWords(advice[1], max(1, width-4)) {
			lines = append(lines, strings.TrimRight("    "+line, " "))
		}
	}
	if value.JetBrainsIDE != "" && value.AutoInstallIDE && !value.JetBrainsInstalled {
		text := "Install the " + value.JetBrainsIDE + " plugin from the JetBrains Marketplace: https://docs.claude.com/s/claude-code-jetbrains"
		measured := exportWrapWords(text, max(1, width-4))
		bodyWidth := 0
		for _, line := range measured {
			bodyWidth = max(bodyWidth, exportWidth(line))
		}
		for i, line := range exportWrapWords(text, max(1, bodyWidth)) {
			prefix := "    "
			if i == 0 {
				prefix = "  ↑ "
			}
			lines = append(lines, strings.TrimRight(prefix+line, " "))
		}
	}
	return lines
}
