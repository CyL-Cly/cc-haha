package sandboxruntime

import (
	"bytes"
	"embed"
	"errors"
	"os"
	"path/filepath"
)

//go:embed assets/seccomp/arm64/* assets/seccomp/amd64/* assets/seccomp/manifest.json
var seccompAssets embed.FS

// InstallSeccompAssets retains the exact pinned native helper, independently
// of an installed Node/CLI package. Only a process-owned absolute asset root is
// accepted. Existing differing bytes are replaced through a private atomic file.
func InstallSeccompAssets(root, architecture string) (bpf, apply string, failure error) {
	if !filepath.IsAbs(root) {
		return "", "", errors.New("seccomp native assets require absolute owned root")
	}
	if architecture != "arm64" && architecture != "amd64" {
		return "", "", nil
	}
	directory := filepath.Join(root, "vendor", "seccomp", architecture)
	// Never chmod or reuse a user-controlled symlink as a trusted executable.
	// Only the explicitly owned asset root and its descendants are materialized.
	if e := os.MkdirAll(filepath.Dir(root), 0700); e != nil {
		return "", "", e
	}
	for _, dir := range []string{root, filepath.Join(root, "vendor"), filepath.Join(root, "vendor", "seccomp"), directory} {
		if info, e := os.Lstat(dir); e == nil {
			if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
				return "", "", errors.New("seccomp native asset directory is not an owned directory")
			}
		} else if !os.IsNotExist(e) {
			return "", "", e
		} else if e := os.Mkdir(dir, 0700); e != nil {
			return "", "", e
		}
	}
	for _, name := range []string{"apply-seccomp", "unix-block.bpf"} {
		data, e := seccompAssets.ReadFile("assets/seccomp/" + architecture + "/" + name)
		if e != nil {
			return "", "", e
		}
		path := filepath.Join(directory, name)
		mode := os.FileMode(0600)
		if name == "apply-seccomp" {
			mode = 0700
		}
		var previous []byte
		info, statError := os.Lstat(path)
		if statError == nil && info.Mode().IsRegular() {
			previous, _ = os.ReadFile(path)
		}
		if !bytes.Equal(previous, data) {
			file, e := os.CreateTemp(directory, ".native-seccomp-")
			if e != nil {
				return "", "", e
			}
			temp := file.Name()
			e = file.Chmod(mode)
			if e == nil {
				_, e = file.Write(data)
			}
			closeError := file.Close()
			if e == nil {
				e = closeError
			}
			if e == nil {
				e = os.Rename(temp, path)
			}
			if e != nil {
				os.Remove(temp)
				return "", "", e
			}
		} else if e = os.Chmod(path, mode); e != nil {
			return "", "", e
		}
	}
	return filepath.Join(directory, "unix-block.bpf"), filepath.Join(directory, "apply-seccomp"), nil
}
