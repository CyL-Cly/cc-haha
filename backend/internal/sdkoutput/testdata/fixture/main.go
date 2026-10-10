package main

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkoutput/testdata/program"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func fail() {
	panic("SDK output fixture requires canonical disposable HOME/config/cwd and trusted allocation root")
}
func main() {
	raw, e := io.ReadAll(io.LimitReader(os.Stdin, 32<<20))
	if e != nil {
		fail()
	}
	var input program.Input
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&input) != nil || input.BaseNow < 0 || input.BaseNow > 253402300799999 {
		fail()
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		fail()
	}
	home := input.Home
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	cwd, _ := os.Getwd()
	canonical, e := filepath.EvalSymlinks(home)
	if e != nil || canonical != home || cwd != home || home != os.Getenv("HOME") || home != os.Getenv("USERPROFILE") || os.Getenv("BUN_OPTIONS") != "--no-env-file" || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(home, ".claude") {
		fail()
	}
	canonicalRoot, e := filepath.EvalSymlinks(root)
	if e != nil || canonicalRoot != root || !strings.HasPrefix(home, root+string(filepath.Separator)) {
		fail()
	}
	temp, _ := filepath.EvalSymlinks(os.TempDir())
	if !strings.HasPrefix(root, temp+string(filepath.Separator)) {
		fail()
	}
	for _, path := range []string{home, filepath.Join(home, ".claude")} {
		info, e := os.Lstat(path)
		if e != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			fail()
		}
	}
	output, e := program.Run(input)
	if e != nil {
		panic(e)
	}
	encoded, e := json.Marshal(map[string]any{"results": output})
	if e != nil {
		panic(e)
	}
	fmt.Print(string(encoded))
}
