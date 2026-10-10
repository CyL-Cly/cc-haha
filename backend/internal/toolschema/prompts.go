package toolschema

import (
	"encoding/json"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"math"
	"math/big"
	"strings"
)

func (session *Session) readPrompt(host Host) (string, error) {
	if host.MainLoopModel == "" {
		return "", unsupported("unresolved Read main-loop model")
	}
	if session.readLimits == nil {
		limits := readPromptLimits{MaxSize: 256 * 1024}
		var config struct {
			MaxSizeBytes           *float64 `json:"maxSizeBytes"`
			IncludeMaxSizeInPrompt bool     `json:"includeMaxSizeInPrompt"`
			TargetedRangeNudge     bool     `json:"targetedRangeNudge"`
		}
		// Invalid feature values fall back independently in Source. Non-number
		// size values must not discard otherwise valid prompt flags.
		var values map[string]json.RawMessage
		_ = json.Unmarshal(host.ReadLimits, &values)
		_ = json.Unmarshal(values["maxSizeBytes"], &config.MaxSizeBytes)
		_ = json.Unmarshal(values["includeMaxSizeInPrompt"], &config.IncludeMaxSizeInPrompt)
		_ = json.Unmarshal(values["targetedRangeNudge"], &config.TargetedRangeNudge)
		if config.MaxSizeBytes != nil && *config.MaxSizeBytes > 0 && !math.IsNaN(*config.MaxSizeBytes) && !math.IsInf(*config.MaxSizeBytes, 0) {
			limits.MaxSize = *config.MaxSizeBytes
		}
		limits.IncludeSize = config.IncludeMaxSizeInPrompt
		limits.Targeted = config.TargetedRangeNudge
		session.readLimits = &limits
	}
	maxInstruction := ""
	if session.readLimits.IncludeSize {
		maxInstruction = ". Files larger than " + formatFileSize(session.readLimits.MaxSize) + " will return an error; use offset and limit for larger files"
	}
	offsetInstruction := "- You can optionally specify a line offset and limit (especially handy for long files), but it's recommended to read the whole file by not providing these parameters"
	if session.readLimits.Targeted {
		offsetInstruction = "- When you already know which part of the file you need, only read that part. This can be important for larger files."
	}
	pdf := ""
	if !strings.Contains(strings.ToLower(host.MainLoopModel), "claude-3-haiku") {
		pdf = "\n- This tool can read PDF files (.pdf). For large PDFs (more than 10 pages), you MUST provide the pages parameter to read specific page ranges (e.g., pages: \"1-5\"). Reading a large PDF without the pages parameter will fail. Maximum 20 pages per request."
	}
	return "Reads a file from the local filesystem. You can access any file directly by using this tool.\nAssume this tool is able to read all files on the machine. If the User provides a path to a file assume that path is valid. It is okay to read a file that does not exist; an error will be returned.\n\nUsage:\n- The file_path parameter must be an absolute path, not a relative path\n- By default, it reads up to 2000 lines starting from the beginning of the file" + maxInstruction + "\n" + offsetInstruction + "\n- Results are returned using cat -n format, with line numbers starting at 1\n- This tool allows Claude Code to read images (eg PNG, JPG, etc). When reading an image file the contents are presented visually as Claude Code is a multimodal LLM." + pdf + "\n- This tool can read Jupyter notebooks (.ipynb files) and returns all cells with their outputs, combining code, text, and visualizations.\n- This tool can only read files, not directories. To read a directory, use an ls command via the Bash tool.\n- You will regularly be asked to read screenshots. If the user provides a path to a screenshot, ALWAYS use this tool to view the file at the path. This tool will work with all temporary file paths.\n- If you read a file that exists but has empty contents you will receive a system reminder warning in place of file contents.", nil
}
func formatFileSize(size float64) string {
	kb := size / 1024
	if kb < 1 {
		return javascriptNumber(size) + " bytes"
	}
	value, unit := kb, "KB"
	if value >= 1024 {
		value /= 1024
		unit = "MB"
	}
	if value >= 1024 {
		value /= 1024
		unit = "GB"
	}
	return strings.TrimSuffix(javascriptFixedOne(value), ".0") + unit
}
func javascriptNumber(value float64) string {
	encoded, _ := json.Marshal(value)
	result, _ := protocol.ParseAndStringifyJavaScriptJSON(encoded)
	return string(result)
}

// ECMAScript toFixed picks the larger integer on an exact midpoint. Go's
// floating formatter uses ties-to-even; the Read 1280-byte prompt exposes it.
func javascriptFixedOne(value float64) string {
	if value >= 1e21 {
		return javascriptNumber(value)
	}
	rational := new(big.Rat).SetFloat64(value)
	rational.Mul(rational, big.NewRat(10, 1))
	integer, remainder := new(big.Int), new(big.Int)
	integer.QuoRem(rational.Num(), rational.Denom(), remainder)
	if remainder.Lsh(remainder, 1).Cmp(rational.Denom()) >= 0 {
		integer.Add(integer, big.NewInt(1))
	}
	digits := integer.String()
	return digits[:len(digits)-1] + "." + digits[len(digits)-1:]
}
func (session *Session) prompt(definition Definition, host Host) (json.RawMessage, error) {
	if SessionCollaborationAction(definition.Name) != "" {
		return definition.Prompt, nil
	}
	if definition.Kind == "registered" {
		return definition.Prompt, nil
	}
	switch definition.Name {
	case "ImageGen", "ImageEdit":
		return definition.Prompt, nil
	case "TeamCreate", "TeamDelete", "TeamPlan":
		return quote(teamPrompt(definition.Name, host.TeamReviewRequired)), nil
	case "EnterWorktree":
		return quote(enterWorktreePrompt), nil
	case "ExitWorktree":
		return quote(exitWorktreePrompt), nil
	case "Agent":
		text, err := agentPrompt(host)
		return quote(text), err
	case "Bash":
		return quote(bashPromptForEnvironment(host.BashEnvironment)), nil
	case "Read":
		text, err := session.readPrompt(host)
		return quote(text), err
	case "Edit":
		prefix := "line number + tab"
		if host.CompactLinePrefixKilled {
			prefix = "spaces + line number + arrow"
		}
		return quote("Performs exact string replacements in files.\n\nUsage:\n- You must use your `Read` tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file. \n- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: " + prefix + ". Everything after that is the actual file content to match. Never include any part of the line number prefix in the old_string or new_string.\n- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.\n- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.\n- The edit will FAIL if `old_string` is not unique in the file. Either provide a larger string with more surrounding context to make it unique or use `replace_all` to change every instance of `old_string`.\n- Use `replace_all` for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance."), nil
	case "Write":
		return quote("Writes a file to the local filesystem.\n\nUsage:\n- This tool will overwrite the existing file if there is one at the provided path.\n- If this is an existing file, you MUST use the Read tool first to read the file's contents. This tool will fail if you did not read the file first.\n- Prefer the Edit tool for modifying existing files — it only sends the diff. Only use this tool to create new files or for complete rewrites.\n- NEVER create documentation files (*.md) or README files unless explicitly requested by the User.\n- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked."), nil
	case "TodoWrite":
		return quote(todoPrompt), nil
	case "WebFetch":
		return quote(webFetchPrompt), nil
	case "WebSearch":
		return quote(webSearchPrompt(host)), nil
	case "AskUserQuestion":
		return quote(questionPrompt(host.QuestionPreviewFormat)), nil
	case "NotebookEdit":
		return quote("Completely replaces the contents of a specific cell in a Jupyter notebook (.ipynb file) with new source. Jupyter notebooks are interactive documents that combine code, text, and visualizations, commonly used for data analysis and scientific computing. The notebook_path parameter must be an absolute path, not a relative path. The cell_number is 0-indexed. Use edit_mode=insert to add a new cell at the index specified by cell_number. Use edit_mode=delete to delete the cell at the index specified by cell_number."), nil
	case "TaskOutput":
		return quote(taskOutputPrompt), nil
	case "TaskStop":
		return quote(taskStopPrompt), nil
	case "TaskCreate", "TaskGet", "TaskUpdate", "TaskList":
		return quote(taskPrompt(definition.Name, host.TeamEnabled)), nil
	case "EnterPlanMode", "ExitPlanMode":
		return quote(planPrompt(definition.Name, host.PlanInterviewPhase, host.PlanUserType)), nil
	case "Skill":
		return quote(skillPrompt), nil
	case "ListMcpResourcesTool":
		return quote(listMCPResourcesPrompt), nil
	case "ReadMcpResourceTool":
		return quote(readMCPResourcePrompt), nil
	case "ToolSearch":
		return toolSearchPrompt(host.ToolSearchDeltaEnabled), nil
	case "Workflow":
		return quote(workflowPrompt(host)), nil
	case "SendMessage":
		return quote(sendMessagePrompt), nil
	case "Glob":
		return quote("- Fast file pattern matching tool that works with any codebase size\n- Supports glob patterns like \"**/*.js\" or \"src/**/*.ts\"\n- Returns matching file paths sorted by modification time\n- Use this tool when you need to find files by name patterns\n- When you are doing an open ended search that may require multiple rounds of globbing and grepping, use the Agent tool instead"), nil
	case "Grep":
		return quote("A powerful search tool built on ripgrep\n\n  Usage:\n  - ALWAYS use Grep for search tasks. NEVER invoke `grep` or `rg` as a Bash command. The Grep tool has been optimized for correct permissions and access.\n  - Supports full regex syntax (e.g., \"log.*Error\", \"function\\s+\\w+\")\n  - Filter files with glob parameter (e.g., \"*.js\", \"**/*.tsx\") or type parameter (e.g., \"js\", \"py\", \"rust\")\n  - Output modes: \"content\" shows matching lines, \"files_with_matches\" shows only file paths (default), \"count\" shows match counts\n  - Use Agent tool for open-ended searches requiring multiple rounds\n  - Pattern syntax: Uses ripgrep (not grep) - literal braces need escaping (use `interface\\{\\}` to find `interface{}` in Go code)\n  - Multiline matching: By default patterns match within single lines only. For cross-line patterns like `struct \\{[\\s\\S]*?field`, use `multiline: true`\n"), nil
	default:
		return nil, unsupported("prompt recipe " + definition.Name)
	}
}
