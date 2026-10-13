package builtintools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/windowshell"
)

func TestPowerShellNativeASTPermissionAndRealToolExecution(t *testing.T) {
	executable := "/opt/homebrew/bin/pwsh"
	if _, err := os.Stat(executable); err != nil {
		t.Skip("physical PowerShell native host required")
	}
	service, owner, home, permissions := fixture(t)
	permissions.Set("mode", quote("default"))
	service.options.Environment["CLAUDE_CODE_POWERSHELL_PATH"] = executable
	service.options.Environment["CLAUDE_CODE_USE_POWERSHELL_TOOL"] = "1"
	service.options.Environment["TMPDIR"] = home
	service.options.Shells = windowshell.NewNative(service.options.Platform, home, service.options.Environment)
	service.options.Shells.Warm(context.Background())
	target := filepath.Join(home, "native-powershell-private.txt")
	if err := os.WriteFile(target, []byte("PRIVATE_OLD\n"), 0600); err != nil {
		t.Fatal(err)
	}
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		read := protocol.NewJSONObject(field("command", "Get-Content '"+target+"'"))
		decision, e := service.PermissionDecision(scope, "PowerShell", &read)
		if e != nil {
			return e
		}
		if decision.Behavior != "allow" {
			return fmt.Errorf("real AST read default decision %+v", decision)
		}
		result, e := service.Call(context.Background(), "PowerShell", read, scope, false)
		if e != nil {
			return e
		}
		if !strings.Contains(string(result), "PRIVATE_OLD") {
			return fmt.Errorf("physical PowerShell output %s", result)
		}
		write := protocol.NewJSONObject(field("command", "Set-Content '"+target+"' PRIVATE_NEW"))
		decision, e = service.PermissionDecision(scope, "PowerShell", &write)
		if e != nil {
			return e
		}
		if decision.Behavior != "ask" {
			return fmt.Errorf("real AST writes need default permission: %+v", decision)
		}
		permissions.Set("mode", quote("acceptEdits"))
		decision, e = service.PermissionDecision(scope, "PowerShell", &write)
		if e != nil {
			return e
		}
		if decision.Behavior != "allow" {
			return fmt.Errorf("same write acceptEdits result %+v", decision)
		}
		if _, e = service.Call(context.Background(), "PowerShell", write, scope, false); e != nil {
			return e
		}
		permissions.Set("alwaysAskRules", raw(map[string][]string{"localSettings": {"PowerShell(Get-Content:*)"}}))
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"localSettings": {"PowerShell(Remove-Item:*)"}}))
		compound := protocol.NewJSONObject(field("command", "Get-Content '"+target+"'; Remove-Item '"+target+"'"))
		decision, e = service.PermissionDecision(scope, "PowerShell", &compound)
		if e != nil {
			return e
		}
		if decision.Behavior != "deny" {
			return fmt.Errorf("actual later deny must beat earlier ask: %+v", decision)
		}
		permissions.Set("alwaysDenyRules", raw(map[string][]string{"localSettings": {"Edit(./native-powershell-private.txt)"}}))
		write = protocol.NewJSONObject(field("command", "Write-Output $env:PRIVATE; Set-Content '"+target+"' FORBIDDEN"))
		decision, e = service.PermissionDecision(scope, "PowerShell", &write)
		if e != nil {
			return e
		}
		if decision.Behavior != "deny" {
			return fmt.Errorf("actual filesystem deny beats earlier security ask: %+v", decision)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(target)
	if err != nil || string(data) != "PRIVATE_NEW\n" {
		t.Fatal("owned real PS write/denied write", err, string(data))
	}
}
