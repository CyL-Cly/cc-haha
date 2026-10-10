package sandboxruntime

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/dop251/goja"
)

func ExpandLinuxGlob(pattern string, paths PathContext) ([]string, error) {
	normalized := NormalizePath(pattern, paths)
	prefix := normalized
	if index := strings.IndexAny(prefix, "*?[]"); index >= 0 {
		prefix = prefix[:index]
	}
	if prefix == "" || prefix == "/" {
		return []string{}, nil
	}
	base := filepath.Dir(prefix)
	if strings.HasSuffix(prefix, "/") {
		base = strings.TrimSuffix(prefix, "/")
	}
	if !exists(base) {
		return []string{}, nil
	}
	// Only the standard RegExp primitive executes in this isolated language VM.
	// The directory walk and all Sandbox business stay native Go.
	vm := goja.New()
	constructor, _ := goja.AssertConstructor(vm.Get("RegExp"))
	expression, e := constructor(nil, vm.ToValue(GlobRegex(normalized)))
	if e != nil {
		return nil, e
	}
	test, _ := goja.AssertFunction(expression.Get("test"))
	result := []string{}
	queue := []string{base}
	for len(queue) > 0 {
		dir := queue[0]
		queue = queue[1:]
		directory, e := os.Open(dir)
		if e != nil {
			// The pinned recursive readdir buffers all entries before matching.
			// A traversal error discards that observation rather than publishing
			// a prefix which the Source never saw.
			return []string{}, nil
		}
		entries, e := directory.ReadDir(-1)
		_ = directory.Close()
		if e != nil {
			// The pinned recursive readdir buffers all entries before matching.
			// A traversal error discards that observation rather than publishing
			// a prefix which the Source never saw.
			return []string{}, nil
		}
		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())
			matched, e := test(expression, vm.ToValue(path))
			if e != nil {
				return nil, e
			}
			if matched.ToBoolean() {
				result = append(result, path)
			}
			isDirectory := entry.IsDir()
			if entry.Type()&os.ModeSymlink != 0 {
				stat, e := os.Stat(path)
				if e != nil {
					if errors.Is(e, syscall.ELOOP) {
						return []string{}, nil
					}
					continue
				}
				isDirectory = stat.IsDir()
			}
			if isDirectory {
				queue = append(queue, path)
			}
		}
	}
	return result, nil
}
