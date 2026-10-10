// Package commandnames owns the Source-compiled builtin command and alias
// membership, independent of UI availability and implemented executors.
package commandnames

import (
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"strings"
)

var externalNames = []string{
	"add-dir",
	"advisor",
	"agent",
	"agents",
	"branch",
	"fork",
	"btw",
	"chrome",
	"clear",
	"reset",
	"new",
	"color",
	"compact",
	"config",
	"settings",
	"copy",
	"desktop",
	"app",
	"context",
	"cost",
	"diff",
	"doctor",
	"effort",
	"exit",
	"quit",
	"fast",
	"files",
	"heapdump",
	"help",
	"ide",
	"init",
	"keybindings",
	"install-github-app",
	"install-slack-app",
	"mcp",
	"memory",
	"mobile",
	"ios",
	"android",
	"model",
	"output-style",
	"remote-env",
	"plugin",
	"plugins",
	"marketplace",
	"pr-comments",
	"release-notes",
	"reload-plugins",
	"rename",
	"resume",
	"continue",
	"session",
	"remote",
	"skills",
	"stats",
	"status",
	"statusline",
	"stickers",
	"tag",
	"team",
	"theme",
	"feedback",
	"bug",
	"goal",
	"review",
	"ultrareview",
	"rewind",
	"checkpoint",
	"security-review",
	"terminal-setup",
	"upgrade",
	"extra-usage",
	"rate-limit-options",
	"usage",
	"insights",
	"vim",
	"buddy",
	"think-back",
	"thinkback-play",
	"permissions",
	"allowed-tools",
	"plan",
	"privacy-settings",
	"hooks",
	"export",
	"sandbox",
	"logout",
	"login",
	"passes",
	"tasks",
	"bashes",
	"workflows",
}

func Names(environment map[string]string) ([]string, error) {
	if environment["USER_TYPE"] == "ant" && environment["IS_DEMO"] == "" {
		return nil, errors.New("internal command-name compilation capability is not migrated")
	}
	thirdParty := false
	for _, key := range []string{"CLAUDE_CODE_USE_BEDROCK", "CLAUDE_CODE_USE_VERTEX", "CLAUDE_CODE_USE_FOUNDRY", "CLAUDE_CODE_USE_AZURE_OPENAI"} {
		switch strings.ToLower(protocol.TrimJavaScriptString(environment[key])) {
		case "1", "true", "yes", "on":
			thirdParty = true
		}
	}
	out := make([]string, 0, len(externalNames))
	for _, name := range externalNames {
		if thirdParty && (name == "login" || name == "logout") {
			continue
		}
		out = append(out, name)
	}
	return out, nil
}

func Set(environment map[string]string) (map[string]bool, error) {
	names, err := Names(environment)
	if err != nil {
		return nil, err
	}
	set := map[string]bool{}
	for _, name := range names {
		set[name] = true
	}
	return set, nil
}
