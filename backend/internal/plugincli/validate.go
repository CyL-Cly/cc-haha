package plugincli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
)

type ValidationIssue struct {
	Path    string `json:"path"`
	Message string `json:"message"`
	Code    string `json:"code,omitempty"`
}
type ValidationResult struct {
	Success  bool              `json:"success"`
	Errors   []ValidationIssue `json:"errors"`
	Warnings []ValidationIssue `json:"warnings"`
	FilePath string            `json:"filePath"`
	FileType string            `json:"fileType"`
}

func validationResult(path, kind string) ValidationResult {
	return ValidationResult{Success: true, Errors: []ValidationIssue{}, Warnings: []ValidationIssue{}, FilePath: path, FileType: kind}
}
func (r *ValidationResult) addError(path, message, code string) {
	r.Errors = append(r.Errors, ValidationIssue{path, message, code})
	r.Success = false
}
func (r *ValidationResult) warn(path, message string) {
	r.Warnings = append(r.Warnings, ValidationIssue{Path: path, Message: message})
}
func validationAbsolute(argument, cwd string) string {
	if !filepath.IsAbs(argument) {
		argument = filepath.Join(cwd, argument)
	}
	return filepath.Clean(argument)
}

// ValidateManifest follows the public linter's path detection, including the
// marketplace-first directory probe and its ENOENT-only fallback.
func ValidateManifest(ctx context.Context, argument, cwd string) (ValidationResult, error) {
	p := validationAbsolute(argument, cwd)
	if err := ctx.Err(); err != nil {
		return ValidationResult{}, err
	}
	info, err := os.Stat(p)
	if err != nil && !os.IsNotExist(err) {
		return ValidationResult{}, validationIOError(err)
	}
	if err == nil && info.IsDir() {
		for _, kind := range []string{"marketplace", "plugin"} {
			r, e := validateManifestFile(ctx, filepath.Join(p, ".claude-plugin", kind+".json"), kind)
			if e != nil {
				return r, e
			}
			if len(r.Errors) == 0 || r.Errors[0].Code != "ENOENT" {
				return r, nil
			}
		}
		r := validationResult(p, "plugin")
		r.addError("directory", "No manifest found in directory. Expected .claude-plugin/marketplace.json or .claude-plugin/plugin.json", "")
		return r, nil
	}
	kind := "plugin"
	if filepath.Base(p) == "marketplace.json" {
		kind = "marketplace"
	} else if filepath.Base(p) != "plugin.json" && filepath.Base(filepath.Dir(p)) != ".claude-plugin" {
		content, e := os.ReadFile(p)
		if os.IsNotExist(e) {
			r := validationResult(p, "plugin")
			r.addError("file", "File not found: "+p, "")
			return r, nil
		}
		if e == nil {
			if data, e := validationJSON(content); e == nil && strings.HasPrefix(strings.TrimSpace(string(data)), "{") && strings.HasPrefix(strings.TrimSpace(string(object(data).Get("plugins"))), "[") {
				kind = "marketplace"
			}
		}
	}
	return validateManifestFile(ctx, p, kind)
}

// RunValidate is the original ordinary CLI formatter, separate from the JSON
// ValidationResult API. The public command has no JSON-output option.
func RunValidate(ctx context.Context, argument, cwd string) (Result, error) {
	r, err := ValidateManifest(ctx, argument, cwd)
	if err != nil {
		return Result{Stderr: "✘ Unexpected error during validation: " + err.Error() + "\n", ExitCode: 2}, nil
	}
	var out strings.Builder
	fmt.Fprintf(&out, "Validating %s manifest: %s\n\n", r.FileType, r.FilePath)
	printValidation(&out, r)
	contents := []ValidationResult{}
	if r.FileType == "plugin" && filepath.Base(filepath.Dir(r.FilePath)) == ".claude-plugin" {
		contents, err = ValidatePluginContents(ctx, filepath.Dir(filepath.Dir(r.FilePath)))
		if err != nil {
			return Result{Stdout: out.String(), Stderr: "✘ Unexpected error during validation: " + err.Error() + "\n", ExitCode: 2}, nil
		}
		for _, row := range contents {
			fmt.Fprintf(&out, "Validating %s: %s\n\n", row.FileType, row.FilePath)
			printValidation(&out, row)
		}
	}
	success, warnings := r.Success, len(r.Warnings) > 0
	for _, row := range contents {
		success = success && row.Success
		warnings = warnings || len(row.Warnings) > 0
	}
	code := 0
	if !success {
		out.WriteString("✘ Validation failed\n")
		code = 1
	} else if warnings {
		out.WriteString("✔ Validation passed with warnings\n")
	} else {
		out.WriteString("✔ Validation passed\n")
	}
	return Result{Stdout: out.String(), ExitCode: code}, nil
}
func printValidation(out *strings.Builder, r ValidationResult) {
	for i, issues := range [][]ValidationIssue{r.Errors, r.Warnings} {
		if len(issues) == 0 {
			continue
		}
		figure, label := "✘", "error"
		if i == 1 {
			figure, label = "⚠", "warning"
		}
		if len(issues) != 1 {
			label += "s"
		}
		fmt.Fprintf(out, "%s Found %d %s:\n\n", figure, len(issues), label)
		for _, issue := range issues {
			fmt.Fprintf(out, "  ❯ %s: %s\n", issue.Path, issue.Message)
		}
		out.WriteByte('\n')
	}
}
func validationErrno(err error) string {
	for _, entry := range []struct {
		value error
		name  string
	}{{syscall.ENOENT, "ENOENT"}, {syscall.EISDIR, "EISDIR"}, {syscall.ENOTDIR, "ENOTDIR"}, {syscall.EACCES, "EACCES"}, {syscall.ELOOP, "ELOOP"}} {
		if errors.Is(err, entry.value) {
			return entry.name
		}
	}
	return ""
}
func validationIOError(err error) error {
	if p, ok := err.(*os.PathError); ok && (p.Op == "stat" || p.Op == "readdirent" || p.Op == "scandir") {
		code := validationErrno(err)
		description := map[string]string{"ENOTDIR": "not a directory", "EACCES": "permission denied", "ELOOP": "too many symbolic links encountered", "ENOENT": "no such file or directory"}[code]
		if description != "" {
			op := p.Op
			if op == "readdirent" {
				op = "scandir"
			}
			return fmt.Errorf("%s: %s, %s '%s'", code, description, op, p.Path)
		}
	}
	return errors.New(strings.TrimPrefix(storage.JavaScriptErrorText(err), "Error: "))
}
