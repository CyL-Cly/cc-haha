package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"sort"
)

const workspaceWatchChunk = 256

type workspaceWatchState struct {
	identity    os.FileInfo
	entries     map[string]string
	large       bool
	fingerprint string
}
type workspaceWatchScan struct {
	path   string
	handle *os.File
	state  workspaceWatchState
	count  int
	digest [32]byte
}

func (scan *workspaceWatchScan) close() {
	if scan.handle != nil {
		_ = scan.handle.Close()
		scan.handle = nil
	}
}
func workspaceWatchSignature(info os.FileInfo) string {
	// Access time is excluded: viewing a file must not itself invalidate previews.
	change := ""
	identity := ""
	value := reflect.ValueOf(info.Sys())
	if value.IsValid() && value.Kind() == reflect.Pointer {
		value = value.Elem()
	}
	if value.IsValid() && value.Kind() == reflect.Struct {
		for _, field := range []string{"Dev", "Ino"} {
			v := value.FieldByName(field)
			if v.IsValid() && v.CanInterface() {
				identity += fmt.Sprint(v.Interface()) + ":"
			}
		}

		for _, field := range []string{"Ctim", "Ctimespec", "ChangeTime"} {
			v := value.FieldByName(field)
			if v.IsValid() && v.CanInterface() {
				change = fmt.Sprint(v.Interface())
				break
			}
		}
	}
	return identity + fmt.Sprintf("%d:%d:%d:%s", info.Mode(), info.Size(), info.ModTime().UnixNano(), change)
}
func (scan *workspaceWatchScan) readChunk(ctx context.Context) (*workspaceWatchState, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scan.handle == nil {
		scan.state.entries = map[string]string{}
		info, e := os.Lstat(scan.path)
		if workspaceMissing(e) {
			return &scan.state, nil
		}
		if e != nil {
			return nil, e
		}
		if !info.IsDir() {
			return nil, fmt.Errorf("Watch path is no longer a directory: %s", scan.path)
		}
		handle, e := os.Open(scan.path)
		if e != nil {
			return nil, e
		}
		scan.handle = handle
		scan.state.identity = info
		opened, e := handle.Stat()
		if e != nil || !os.SameFile(info, opened) {
			scan.close()
			return nil, fmt.Errorf("Watch directory changed while opening: %s", scan.path)
		}
	}
	entries, e := scan.handle.ReadDir(workspaceWatchChunk)
	if e != nil && e != io.EOF {
		scan.close()
		return nil, e
	}
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			scan.close()
			return nil, err
		}
		if vcsDirectory(entry.Name()) {
			continue
		}
		info, err := os.Lstat(filepath.Join(scan.path, entry.Name()))
		if workspaceMissing(err) {
			continue
		}
		if err != nil {
			scan.close()
			return nil, err
		}
		signature := workspaceWatchSignature(info)
		raw, _ := json.Marshal([]string{entry.Name(), signature})
		hash := sha256.Sum256(raw)
		carry := uint16(0)
		for i := len(hash) - 1; i >= 0; i-- {
			sum := uint16(scan.digest[i]) + uint16(hash[i]) + carry
			scan.digest[i] = byte(sum)
			carry = sum >> 8
		}
		scan.count++
		if scan.count <= workspaceWatchChunk {
			scan.state.entries[entry.Name()] = signature
		} else {
			scan.state.large = true
			clear(scan.state.entries)
		}
	}
	if e != io.EOF && len(entries) == workspaceWatchChunk {
		return nil, nil
	}
	scan.state.fingerprint = fmt.Sprintf("%d:%s", scan.count, hex.EncodeToString(scan.digest[:]))
	scan.close()
	return &scan.state, nil
}
func readWorkspaceWatchState(ctx context.Context, path string) (*workspaceWatchState, error) {
	scan := &workspaceWatchScan{path: path}
	defer scan.close()
	for {
		state, e := scan.readChunk(ctx)
		if e != nil || state != nil {
			return state, e
		}
	}
}
func changedWorkspaceWatchEntries(before, after *workspaceWatchState) []string {
	if before.large || after.large {
		return nil
	}
	names := map[string]bool{}
	for name := range before.entries {
		names[name] = true
	}
	for name := range after.entries {
		names[name] = true
	}
	out := []string{}
	for name := range names {
		if before.entries[name] != after.entries[name] {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}
func sameWorkspaceWatchIdentity(before, after os.FileInfo) bool {
	return before == nil && after == nil || before != nil && after != nil && os.SameFile(before, after)
}
