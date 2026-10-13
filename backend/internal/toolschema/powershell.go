package toolschema

import "github.com/NanmiCoder/cc-haha/backend/internal/windowshell"

func PowerShellDefinition(env map[string]string) Definition {
	fields := []Field{{"command", textSchema("The PowerShell command to execute", false)}, {"timeout", numberSchema("Optional timeout in milliseconds (max " + windowshell.NumberString(windowshell.MaxTimeout(env)) + ")")}, {"description", textSchema("Clear, concise description of what this command does in active voice.", true)}}
	if !windowshell.Truthy(env["CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"]) {
		fields = append(fields, Field{"run_in_background", boolSchema("Set to true to run this command in the background. Use Read to read the output later.")})
	}
	fields = append(fields, Field{"dangerouslyDisableSandbox", boolSchema("Set this to true to dangerously override sandbox mode and run commands without sandboxing.")})
	return Definition{Kind: "builtin", Name: "PowerShell", Strict: true, SearchHint: quote("execute Windows PowerShell commands"), InputSchema: &Schema{Kind: "object", UnknownKeys: "strict", Fields: fields}}
}
