package opentargets

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestDuplicateCopiesRetainSourceCanonicalDefaultInstallDepthAndUTF16Rules(t *testing.T) {
	home, row := fixture(t, "darwin")
	left := app("/Downloads/Z.app", "canonical-left", "Z", false, 1)
	right := app("/Applications/A.app", "canonical-left", "A", true, 5)
	canonical := left.AppPath
	left.CanonicalPath = &canonical
	pairs := [][2]Application{
		{left, right},
		{app("/Applications/A.app", "default", "A", false, 3), app("/Downloads/Z.app", "default", "Z", true, 1)},
		{app("/Downloads/A.app", "tier", "A", false, 50), app("/Applications/Z.app", "tier", "Z", false, 1)},
		{app("/Applications/Nested/A.app", "depth", "A", false, 50), app("/Applications/Z.app", "depth", "Z", false, 1)},
		{app("/Downloads/Z.app", "lex", "Z", false, 50), app("/Downloads/A.app", "lex", "A", false, 1)},
		{app("/Downloads/\ue000.app", "utf16", "first", false, 50), app("/Downloads/😀.app", "utf16", "second", false, 1)},
		{app("/Applications/Z.app", "reverse-tier", "Z", false, 1), app("/Downloads/A.app", "reverse-tier", "A", false, 50)},
		{app("/Applications/Z.app", "reverse-depth", "Z", false, 1), app("/Applications/Nested/A.app", "reverse-depth", "A", false, 50)},
		{app("/Downloads/A.app", "reverse-lex", "A", false, 1), app("/Downloads/Z.app", "reverse-lex", "Z", false, 50)},
		{app("/Downloads/A.app", "prefix-lex", "A", false, 1), app("/Downloads/A.app-more", "prefix-lex", "more", false, 50)},
		{app("/Downloads/A.app-more", "reverse-prefix-lex", "more", false, 50), app("/Downloads/A.app", "reverse-prefix-lex", "A", false, 1)},
	}
	for _, pair := range pairs {
		file := filepath.Join(row.CWD, pair[0].BundleIDString()+".txt")
		ownedFile(t, file, 0600)
		row.Native.Applications[file] = jsonApps([]Application{pair[0], pair[1]}, nil)
		row.Operations = append(row.Operations, queryOp(file))
	}
	_, out := runScenario(t, home, row)
	for i, item := range out.Observations {
		var list List
		if err := json.Unmarshal([]byte(item.Body), &list); err != nil {
			t.Fatal(err)
		}
		if len(list.Targets) != 3 {
			t.Fatalf("duplicate copy %d survived %+v", i, list)
		}
	}
}
func (a Application) BundleIDString() string {
	if a.BundleID != nil {
		return *a.BundleID
	}
	return "nil"
}

func TestWindowsUNCDriveRootWrappersAndTildeFilesAgainstSource(t *testing.T) {
	for _, rowPath := range []struct{ command, application string }{{`\\server\share\bin\code.cmd`, `\\server\share\Code.exe`}, {`C:\code.cmd`, `C:\Code.exe`}, {`\code.cmd`, `\Code.exe`}} {
		home, row := fixture(t, "win32")
		ownedFile(t, filepath.Join(home, "report.txt"), 0600)
		row.Native.Commands["code.cmd"] = rowPath.command
		row.Native.Exists = []string{rowPath.application}
		row.Operations = []operation{{Method: "GET", Path: "/api/open-targets"}, openOp("vscode", "~/report.txt"), openOp("explorer", row.CWD)}
		_, out := runScenario(t, home, row)
		if len(out.Launches) != 2 || out.Launches[0].Command != rowPath.application {
			t.Fatalf("wrapper resolution %+v", out)
		}
	}
}
func TestNativeFileDiscoveryIconReadAndExplicitConstruction(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture replaces command lookup primitive")
	}
	home, row := fixture(t, "linux")
	bin := filepath.Join(home, "bin")
	ownedFile(t, filepath.Join(bin, "which"), 0700)
	if err := os.WriteFile(filepath.Join(bin, "which"), []byte("#!/bin/sh\nprintf '%s' \"$CODE\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	ownedFile(t, filepath.Join(bin, "code"), 0700)
	icon := filepath.Join(home, "native-icon.png")
	ownedFile(t, icon, 0600)
	desktop := filepath.Join(home, ".local", "share", "applications", "vscode.desktop")
	ownedFile(t, desktop, 0600)
	if err := os.WriteFile(desktop, []byte("[Desktop Entry]\nName=VS Code\nExec=code %F\nIcon="+icon+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := New(Options{Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: row.CWD, Platform: "linux", Environment: map[string]string{"PATH": bin, "CODE": filepath.Join(bin, "code"), "TEMP": filepath.Join(home, "tmp")}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.GetIcon(context.Background(), "vscode", 64)
	if err != nil || string(result.Data) != "content" {
		t.Fatalf("actual native Linux file discovery/icon %q %v", result.Data, err)
	}
	if value := s.primitives.ReadTextFile(filepath.Join(home, "missing")); value != nil {
		t.Fatal("missing native text file was invented")
	}
	if names := s.primitives.ReadDirNames(filepath.Join(home, "missing")); len(names) != 0 {
		t.Fatal(names)
	}
	if list, err := s.primitives.ListApplicationsForFile(context.Background(), desktop); err != nil || len(list.Applications) != 0 {
		t.Fatal(list, err)
	}
	for _, o := range []Options{{}, {Home: home, ConfigDir: filepath.Join(home, ".claude"), CWD: row.CWD, Platform: "linux", Environment: map[string]string{}, TempDir: "relative"}} {
		if _, err := New(o); err == nil {
			t.Fatal("ambient/relative native configuration accepted")
		}
	}
	if err := apiError(400, "exact source message", "BAD_REQUEST"); err.Error() != "exact source message" {
		t.Fatal(err)
	}
}
