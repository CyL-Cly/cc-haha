package storage

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"syscall"
	"testing"
)

type sourceNamedError struct{ name string }

func (err sourceNamedError) Error() string     { return "fixture cause" }
func (err sourceNamedError) ErrorName() string { return err.name }

func TestJavaScriptErrorTextKeepsCauseAndOnlyRendersKnownBusinessForms(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{nil, "null"},
		{errors.New("fixture cause"), "Error: fixture cause"},
		{sourceNamedError{"AbortError"}, "AbortError: fixture cause"},
		{sourceNamedError{""}, "fixture cause"},
		{&os.PathError{Op: "read", Path: "/temporary/dir", Err: syscall.EISDIR}, "Error: EISDIR: illegal operation on a directory, read"},
		{&os.PathError{Op: "open", Path: "/temporary/denied", Err: fs.ErrPermission}, "Error: EACCES: permission denied, open '/temporary/denied'"},
		{&os.PathError{Op: "open", Path: "/temporary/dir", Err: syscall.EISDIR}, "Error: EISDIR: illegal operation on a directory, open '/temporary/dir'"},
		{&os.PathError{Op: "mkdir", Path: "/temporary/file", Err: fs.ErrExist}, "Error: EEXIST: file already exists, mkdir '/temporary/file'"},
		{&os.PathError{Op: "open", Path: "/temporary/file/child", Err: syscall.ENOTDIR}, "Error: ENOTDIR: not a directory, open '/temporary/file/child'"},
		{&os.PathError{Op: "open", Path: "/temporary/missing", Err: fs.ErrNotExist}, "Error: ENOENT: no such file or directory, open '/temporary/missing'"},
		{&os.LinkError{Op: "rename", Old: "/temporary/missing", New: "/temporary/next", Err: fs.ErrNotExist}, "Error: ENOENT: no such file or directory, rename '/temporary/missing' -> '/temporary/next'"},
		{&os.LinkError{Op: "rename", Old: "/temporary/file", New: "/temporary/dir", Err: syscall.EISDIR}, "Error: EISDIR: illegal operation on a directory, rename '/temporary/file' -> '/temporary/dir'"},
	}
	for _, test := range cases {
		if actual := JavaScriptErrorText(test.err); actual != test.want {
			t.Fatalf("error rendering changed: %q, want %q", actual, test.want)
		}
	}
	for _, err := range []error{
		&os.PathError{Op: "unobserved", Path: "/temporary/file", Err: fs.ErrPermission},
		&os.PathError{Op: "open", Path: "/temporary/file", Err: errors.New("unobserved cause")},
		&os.PathError{Op: "open", Path: "/temporary/file", Err: syscall.EPERM},
		&os.PathError{Op: "open", Path: "/temporary/file", Err: syscall.ENOTEMPTY},
		&os.LinkError{Op: "link", Old: "/temporary/a", New: "/temporary/b", Err: fs.ErrPermission},
		&os.LinkError{Op: "rename", Old: "/temporary/a", New: "/temporary/b", Err: errors.New("unobserved cause")},
		fmt.Errorf("wrapped: %w", &os.PathError{Op: "open", Path: "/temporary/file", Err: fs.ErrPermission}),
	} {
		if actual := JavaScriptErrorText(err); actual != "Error: "+err.Error() {
			t.Fatal("unobserved/wrapped error was silently rewritten", actual)
		}
	}
	denied := &os.PathError{Op: "open", Path: "/temporary/file", Err: fs.ErrPermission}
	before := denied.Error()
	_ = JavaScriptErrorText(denied)
	if denied.Error() != before || !errors.Is(denied, fs.ErrPermission) || denied.Unwrap() != fs.ErrPermission {
		t.Fatal("rendering replaced or mutated native cause")
	}
}

func TestNativePrecheckRenderingRequiresKnownRequestedPathAndSourceKind(t *testing.T) {
	direct := &os.PathError{Op: "mkdir", Path: "/temporary/file", Err: syscall.ENOTDIR}
	if actual := JavaScriptErrorText(direct); actual != "Error: ENOTDIR: not a directory, mkdir '/temporary/file'" {
		t.Fatal("missing requested-path provenance invented an errno", actual)
	}
	for _, requested := range []string{"/temporary/file", "/temporary/file/child", "/temporary/file/child/grandchild"} {
		code, description := "ENOTDIR", "not a directory"
		if requested == direct.Path {
			code, description = "EEXIST", "file already exists"
		}
		want := "Error: " + code + ": " + description + ", mkdir '" + requested + "'"
		if actual := JavaScriptErrorText(direct, JavaScriptErrorContext{RequestedPath: requested}); actual != want {
			t.Fatalf("Go recursive precheck lost original operation path: %s", actual)
		}
	}
	rename := &os.LinkError{Op: "rename", Old: "/temporary/file", New: "/temporary/dir", Err: syscall.EEXIST}
	if actual := JavaScriptErrorText(rename); actual != "Error: EEXIST: file already exists, rename '/temporary/file' -> '/temporary/dir'" {
		t.Fatal("unknown rename source kind invented an errno", actual)
	}
	if actual := JavaScriptErrorText(rename, JavaScriptErrorContext{RenameSourceIsFile: true}); actual != "Error: EISDIR: illegal operation on a directory, rename '/temporary/file' -> '/temporary/dir'" {
		t.Fatal("known temporary-file rename precheck differs", actual)
	}
	if !errors.Is(direct, syscall.ENOTDIR) || !errors.Is(rename, syscall.EEXIST) {
		t.Fatal("rendering replaced the original native precheck cause")
	}
}
