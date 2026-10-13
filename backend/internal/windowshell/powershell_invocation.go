package windowshell

import "strings"

func PowerShellCommand(command, cwdFilePath string) string {
	escaped := strings.ReplaceAll(cwdFilePath, "'", "''")
	return command + "\n; $_ec = if ($null -ne $LASTEXITCODE) { $LASTEXITCODE } elseif ($?) { 0 } else { 1 }\n; (Get-Location).Path | Out-File -FilePath '" + escaped + "' -Encoding utf8 -NoNewline\n; exit $_ec"
}

func PowerShellArgs(command string) []string {
	return []string{"-NoProfile", "-NonInteractive", "-Command", command}
}
