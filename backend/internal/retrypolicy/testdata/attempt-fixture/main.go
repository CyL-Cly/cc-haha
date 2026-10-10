package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/retrypolicy/testdata/attempt-fixture/driver"
)

func main() {
	var input driver.Input
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		panic(err)
	}
	cwd, err := os.Getwd()
	root := os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT")
	canonicalHome, homeErr := filepath.EvalSymlinks(input.Home)
	canonicalRoot, rootErr := filepath.EvalSymlinks(root)
	relative, relativeErr := filepath.Rel(canonicalRoot, canonicalHome)
	if err != nil || homeErr != nil || rootErr != nil || relativeErr != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) || root == "" || canonicalHome != cwd || input.Home != os.Getenv("HOME") || input.Home != os.Getenv("USERPROFILE") || filepath.Join(input.Home, ".claude") != os.Getenv("CLAUDE_CONFIG_DIR") || os.Getenv("CC_HAHA_RETRY_ATTEMPT_FIXTURE") != "1" || os.Getenv("BUN_OPTIONS") != "--no-env-file" {
		panic("retry attempt fixture requires disposable HOME/config before business execution")
	}
	for _, path := range []string{input.Home, filepath.Join(input.Home, ".claude")} {
		stat, err := os.Lstat(path)
		if err != nil || !stat.IsDir() || stat.Mode()&os.ModeSymlink != 0 {
			panic("retry attempt fixture requires nonsymlink private directories")
		}
	}
	raw, err := json.Marshal(map[string]any{"observation": driver.Observe(input)})
	if err != nil {
		panic(err)
	}
	fmt.Print(string(raw))
}
