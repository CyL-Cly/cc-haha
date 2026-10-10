package settings

import "testing"

func TestWindowsBatchQuotingMatchesExistingSpawnBoundary(t *testing.T) {
	for _, fixture := range []struct{ argument, escaped string }{
		{"", `^"^"`},
		{"check-ignore", `^"check-ignore^"`},
		{".claude/settings.local.json", `^".claude/settings.local.json^"`},
		{"space & pipe| (parenthesis) %ENV% !bang!", `^"space^ ^&^ pipe^|^ ^(parenthesis^)^ ^%ENV^%^ ^!bang^!^"`},
		{`C:\path\`, `^"C:\path\\^"`},
		{`embedded"quote`, `^"embedded\^"quote^"`},
		{`backslash\"quote`, `^"backslash\\\^"quote^"`},
	} {
		if escaped := windowsBatchArgument(fixture.argument, false); escaped != fixture.escaped {
			t.Errorf("escape %q = %q, want %q", fixture.argument, escaped, fixture.escaped)
		}
	}
	line := windowsBatchCommandLine(`C:\Windows\System32\cmd.exe`, `C:\Program Files\Git\git.cmd`, []string{"check-ignore", ".claude/settings.local.json"})
	if line != `"C:\Windows\System32\cmd.exe" /d /s /c "C:\Program^ Files\Git\git.cmd ^"check-ignore^" ^".claude/settings.local.json^""` {
		t.Fatalf("batch command line = %s", line)
	}
	if got := windowsBatchArgument("&", true); got != `^^^"^^^&^^^"` {
		t.Fatalf("cmd shim double escape = %q", got)
	}
	shim := windowsBatchCommandLine("cmd.exe", `C:\fixture\node_modules\.bin\git.cmd`, []string{"&"})
	if shim != `"cmd.exe" /d /s /c "C:\fixture\node_modules\.bin\git.cmd ^^^"^^^&^^^""` {
		t.Fatalf("cmd shim contract = %s", shim)
	}
}
