package application

import "testing"

func TestPublicStructuredSDKStdioPermissionPromptUsesExplicitOriginalOption(t *testing.T) {
	args := []string{"-p", "--input-format", "stream-json", "--output-format", "stream-json", "--verbose", "--permission-prompt-tool", "stdio"}
	command, parsed, err := ParseCommand(args, "fixture", t.TempDir(), t.TempDir())
	if err != nil || parsed.Parsed == nil || !command.Print {
		t.Fatal("public original SDK stdio permission prompt rejected", err)
	}
	if command.Arguments.Tools != nil || command.Arguments.SystemPrompt != "" || command.Arguments.PermissionMode != "" || command.Arguments.DangerouslySkipPermissions {
		t.Fatal("SDK stdio option changed default Harness or permission mode")
	}
}
