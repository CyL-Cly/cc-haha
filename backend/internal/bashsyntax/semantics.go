package bashsyntax

import "strings"

type SemanticResult struct {
	OK     bool         `json:"ok"`
	Reason SecurityText `json:"reason,omitempty"`
}

func semanticFailure(reason SecurityText) SemanticResult { return SemanticResult{false, reason} }

var zshBuiltins = stringSet("zmodload emulate sysopen sysread syswrite sysseek zpty ztcp zsocket zf_rm zf_mv zf_ln zf_chmod zf_chown zf_mkdir zf_rmdir zf_chgrp")
var evalBuiltins = stringSet("eval source . exec command builtin fc coproc noglob nocorrect trap enable mapfile readarray hash bind complete compgen alias let")
var subscriptEvalFlags = map[SecurityText][]SecurityText{"test": {"-v", "-R"}, "[": {"-v", "-R"}, "[[": {"-v", "-R"}, "printf": {"-v"}, "read": {"-a"}, "unset": {"-v"}, "wait": {"-p"}}
var testArithmeticOps = stringSet("-eq -ne -lt -le -gt -ge")
var readDataFlags = stringSet("-p -d -n -N -t -u -i")
var shellKeywords = stringSet("if then elif else fi while until for in do done case esac function select")

func containsText(s SecurityText, part string) bool  { return strings.Contains(string(s), part) }
func hasPrefixText(s SecurityText, part string) bool { return strings.HasPrefix(string(s), part) }
func anyMatches(args []SecurityText, pattern string) bool {
	for _, arg := range args {
		if matches(pattern, arg) {
			return true
		}
	}
	return false
}

// CheckSemantics ports the original separate post-argv stage; callers do not
// confuse successful tokenization with permission approval.
func CheckSemantics(commands []SecurityCommand) SemanticResult {
	for _, cmd := range commands {
		a := cmd.Argv
		for len(a) > 0 {
			switch a[0] {
			case "time", "nohup":
				a = a[1:]
				continue
			case "timeout":
				i := 1
				for i < len(a) {
					arg := a[i]
					next := SecurityText("")
					if i+1 < len(a) {
						next = a[i+1]
					}
					if arg == "--foreground" || arg == "--preserve-status" || arg == "--verbose" {
						i++
					} else if matches(`^--(?:kill-after|signal)=[A-Za-z0-9_.+-]+$`, arg) {
						i++
					} else if (arg == "--kill-after" || arg == "--signal") && next != "" && matches(`^[A-Za-z0-9_.+-]+$`, next) {
						i += 2
					} else if hasPrefixText(arg, "--") {
						return semanticFailure("timeout with " + arg + " flag cannot be statically analyzed")
					} else if arg == "-v" {
						i++
					} else if (arg == "-k" || arg == "-s") && next != "" && matches(`^[A-Za-z0-9_.+-]+$`, next) {
						i += 2
					} else if matches(`^-[ks][A-Za-z0-9_.+-]+$`, arg) {
						i++
					} else if hasPrefixText(arg, "-") {
						return semanticFailure("timeout with " + arg + " flag cannot be statically analyzed")
					} else {
						break
					}
				}
				if i < len(a) && a[i] != "" && matches(`^\d+(?:\.\d+)?[smhd]?$`, a[i]) {
					a = a[i+1:]
					continue
				}
				if i < len(a) && a[i] != "" {
					return semanticFailure("timeout duration '" + a[i] + "' cannot be statically analyzed")
				}
			case "nice":
				if len(a) > 2 && a[1] == "-n" && a[2] != "" && matches(`^-?\d+$`, a[2]) {
					a = a[3:]
				} else if len(a) > 1 && a[1] != "" && matches(`^-\d+$`, a[1]) {
					a = a[2:]
				} else if len(a) > 1 && a[1] != "" && matches("[$(`]", a[1]) {
					return semanticFailure("nice argument '" + a[1] + "' contains expansion — cannot statically determine wrapped command")
				} else {
					a = a[1:]
				}
				continue
			case "env":
				i := 1
				for i < len(a) {
					arg := a[i]
					if containsText(arg, "=") && !hasPrefixText(arg, "-") {
						i++
					} else if arg == "-i" || arg == "-0" || arg == "-v" {
						i++
					} else if arg == "-u" && i+1 < len(a) && a[i+1] != "" {
						i += 2
					} else if hasPrefixText(arg, "-") {
						return semanticFailure("env with " + arg + " flag cannot be statically analyzed")
					} else {
						break
					}
				}
				if i < len(a) {
					a = a[i:]
					continue
				}
			case "stdbuf":
				i := 1
				for i < len(a) {
					arg := a[i]
					if matches(`^-[ioe]$`, arg) && i+1 < len(a) && a[i+1] != "" {
						i += 2
					} else if matches(`^-[ioe].`, arg) {
						i++
					} else if matches(`^--(input|output|error)=`, arg) {
						i++
					} else if hasPrefixText(arg, "-") {
						return semanticFailure("stdbuf with " + arg + " flag cannot be statically analyzed")
					} else {
						break
					}
				}
				if i > 1 && i < len(a) {
					a = a[i:]
					continue
				}
			}
			break
		}
		if len(a) == 0 {
			continue
		}
		name := a[0]
		if name == "" {
			return semanticFailure("Empty command name — argv[0] may not reflect what bash runs")
		}
		if containsPlaceholder(name) {
			return semanticFailure("Command name is runtime-determined (placeholder argv[0])")
		}
		if hasPrefixText(name, "-") || hasPrefixText(name, "|") || hasPrefixText(name, "&") {
			return semanticFailure("Command appears to be an incomplete fragment")
		}
		if flags, ok := subscriptEvalFlags[name]; ok {
			for i := 1; i < len(a); i++ {
				arg := a[i]
				next := SecurityText("")
				if i+1 < len(a) {
					next = a[i+1]
				}
				for _, flag := range flags {
					if arg == flag && containsText(next, "[") {
						return semanticFailure("'" + name + " " + arg + "' operand contains array subscript — bash evaluates $(cmd) in subscripts")
					}
				}
				if textLen(arg) > 2 && hasPrefixText(arg, "-") && !hasPrefixText(arg, "--") && !containsText(arg, "[") {
					for _, flag := range flags {
						if textLen(flag) == 2 && containsText(arg, string(textSlice(flag, 1, 2))) && containsText(next, "[") {
							return semanticFailure("'" + name + " " + flag + "' (combined in '" + arg + "') operand contains array subscript — bash evaluates $(cmd) in subscripts")
						}
					}
				}
				for _, flag := range flags {
					if textLen(flag) == 2 && hasPrefixText(arg, string(flag)) && textLen(arg) > 2 && containsText(arg, "[") {
						return semanticFailure("'" + name + " " + flag + "' (fused) operand contains array subscript — bash evaluates $(cmd) in subscripts")
					}
				}
			}
		}
		if name == "[[" {
			for i := 2; i < len(a); i++ {
				if !testArithmeticOps[string(a[i])] {
					continue
				}
				if containsText(a[i-1], "[") || i+1 < len(a) && containsText(a[i+1], "[") {
					return semanticFailure("'[[ ... " + a[i] + " ... ]]' operand contains array subscript — bash arithmetically evaluates $(cmd) in subscripts")
				}
			}
		}
		if name == "read" || name == "unset" {
			skip := false
			for _, arg := range a[1:] {
				if skip {
					skip = false
					continue
				}
				if hasPrefixText(arg, "-") {
					if name == "read" {
						if readDataFlags[string(arg)] {
							skip = true
						} else if textLen(arg) > 2 && !hasPrefixText(arg, "--") {
							units := []rune(arg)
							for j := 1; j < len(units); j++ {
								if readDataFlags["-"+string(units[j])] {
									if j == len(units)-1 {
										skip = true
									}
									break
								}
							}
						}
					}
					continue
				}
				if containsText(arg, "[") {
					return semanticFailure("'" + name + "' positional NAME '" + arg + "' contains array subscript — bash evaluates $(cmd) in subscripts")
				}
			}
		}
		if shellKeywords[string(name)] {
			return semanticFailure("Shell keyword '" + name + "' as command name — tree-sitter mis-parse")
		}
		for _, arg := range cmd.Argv {
			if containsText(arg, "\n") && matches(`\n[ \t]*#`, arg) {
				return semanticFailure("Newline followed by # inside a quoted argument can hide arguments from path validation")
			}
		}
		for _, ev := range cmd.EnvVars {
			if containsText(ev.Value, "\n") && matches(`\n[ \t]*#`, ev.Value) {
				return semanticFailure("Newline followed by # inside an env var value can hide arguments from path validation")
			}
		}
		for _, r := range cmd.Redirects {
			if containsText(r.Target, "\n") && matches(`\n[ \t]*#`, r.Target) {
				return semanticFailure("Newline followed by # inside a redirect target can hide arguments from path validation")
			}
		}
		if name == "jq" {
			if anyMatches(a, `\bsystem\s*\(`) {
				return semanticFailure("jq command contains system() function which executes arbitrary commands")
			}
			if anyMatches(a, `^(?:-[fL](?:$|[^A-Za-z])|--(?:from-file|rawfile|slurpfile|library-path)(?:$|=))`) {
				return semanticFailure("jq command contains dangerous flags that could execute code or read arbitrary files")
			}
		}
		if zshBuiltins[string(name)] {
			return semanticFailure("Zsh builtin '" + name + "' can bypass security checks")
		}
		if evalBuiltins[string(name)] {
			safe := name == "command" && len(a) > 1 && (a[1] == "-v" || a[1] == "-V") || name == "fc" && !anyMatches(a[1:], `^-[^-]*[es]`) || name == "compgen" && !anyMatches(a[1:], `^-[^-]*[CFW]`)
			if !safe {
				return semanticFailure("'" + name + "' evaluates arguments as shell code")
			}
		}
		for _, arg := range cmd.Argv {
			if containsText(arg, "/proc/") && matches(`/proc/.*/environ`, arg) {
				return semanticFailure("Accesses /proc/*/environ which may expose secrets")
			}
		}
		for _, r := range cmd.Redirects {
			if containsText(r.Target, "/proc/") && matches(`/proc/.*/environ`, r.Target) {
				return semanticFailure("Accesses /proc/*/environ which may expose secrets")
			}
		}
	}
	return SemanticResult{OK: true}
}
