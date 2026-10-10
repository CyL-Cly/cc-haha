package main

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/diagnostics"
	"io/fs"
)

// A private fixture guard over the existing synchronous native filesystem.
// Every admitted operation delegates once; no Source observations are inputs.
type privateDiagnostics struct{ home string }

func (f privateDiagnostics) MkdirAll(p string, mode fs.FileMode) error {
	if err := guard(f.home, p); err != nil {
		return err
	}
	return (diagnostics.OSFileSystem{}).MkdirAll(p, mode)
}
func (f privateDiagnostics) Lstat(p string) (fs.FileInfo, error) {
	if err := guard(f.home, p); err != nil {
		return nil, err
	}
	return (diagnostics.OSFileSystem{}).Lstat(p)
}
func (f privateDiagnostics) Stat(p string) (fs.FileInfo, error) {
	if err := guard(f.home, p); err != nil {
		return nil, err
	}
	return (diagnostics.OSFileSystem{}).Stat(p)
}
func (f privateDiagnostics) Exists(p string) bool {
	if guard(f.home, p) != nil {
		return false
	}
	return (diagnostics.OSFileSystem{}).Exists(p)
}
func (f privateDiagnostics) Chmod(p string, mode fs.FileMode) error {
	if err := guard(f.home, p); err != nil {
		return err
	}
	return (diagnostics.OSFileSystem{}).Chmod(p, mode)
}
func (f privateDiagnostics) Append(p string, b []byte, mode fs.FileMode) error {
	if err := guard(f.home, p); err != nil {
		return err
	}
	return (diagnostics.OSFileSystem{}).Append(p, b, mode)
}
func (f privateDiagnostics) Rename(a, b string) error {
	if err := guard(f.home, a); err != nil {
		return err
	}
	if err := guard(f.home, b); err != nil {
		return err
	}
	return (diagnostics.OSFileSystem{}).Rename(a, b)
}
func (f privateDiagnostics) ReadDir(p string) ([]string, error) {
	if err := guard(f.home, p); err != nil {
		return nil, err
	}
	return (diagnostics.OSFileSystem{}).ReadDir(p)
}
func (f privateDiagnostics) Remove(p string) error {
	if err := guard(f.home, p); err != nil {
		return err
	}
	return (diagnostics.OSFileSystem{}).Remove(p)
}
