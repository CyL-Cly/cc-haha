package localindex

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	sqlite "modernc.org/sqlite"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

const MaxBackups = 3
const MaxBackupBytes int64 = 256 * 1024 * 1024

type BackupOptions struct {
	Scope, DatabasePath, Reason string
	Now                         func() time.Time
	Rename                      func(string, string) error
	Token                       func() (string, error)
}
type BackupResult struct {
	BackupPath     string `json:"backupPath"`
	MovedFiles     int    `json:"movedFiles"`
	RemovedBackups int    `json:"removedBackups"`
}

func ClassifyFailure(err error) string {
	code := errorCode(err, "")
	var sqlerr *sqlite.Error
	if errors.As(err, &sqlerr) {
		switch sqlerr.Code() & 255 {
		case 3, 8, 10, 13, 14:
			return "DISK_WRITE_FAILED"
		case 5, 6:
			return "SQLITE_BUSY"
		case 11, 26:
			return "SQLITE_CORRUPT"
		}
	}
	if code == "" {
		switch {
		case errors.Is(err, syscall.ENOSPC), errors.Is(err, syscall.EROFS), errors.Is(err, syscall.EACCES), errors.Is(err, syscall.EPERM):
			code = "DISK_WRITE_FAILED"
		}
	}
	switch code {
	case "SQLITE_CORRUPT", "SQLITE_BUSY", "SCHEMA_UNSUPPORTED", "DISK_WRITE_FAILED", "SOURCE_PARSE_DEGRADED", "LOCAL_INDEX_START_FAILED":
		return code
	case "LOCAL_INDEX_SCHEMA_UNSUPPORTED":
		return "SCHEMA_UNSUPPORTED"
	case "LOCAL_INDEX_BACKUP_FAILED":
		return "DISK_WRITE_FAILED"
	}
	if strings.HasPrefix(code, "SQLITE_CORRUPT") || code == "SQLITE_NOTADB" {
		return "SQLITE_CORRUPT"
	}
	if strings.HasPrefix(code, "SQLITE_BUSY") || strings.HasPrefix(code, "SQLITE_LOCKED") {
		return "SQLITE_BUSY"
	}
	for _, prefix := range []string{"SQLITE_READONLY", "SQLITE_FULL", "SQLITE_IOERR", "SQLITE_CANTOPEN", "SQLITE_PERM"} {
		if strings.HasPrefix(code, prefix) {
			return "DISK_WRITE_FAILED"
		}
	}
	return "LOCAL_INDEX_START_FAILED"
}

func backupToken() (string, error) {
	b := make([]byte, 4)
	_, e := rand.Read(b)
	return hex.EncodeToString(b), e
}

// BackupDatabaseFamily moves only regenerable primary-index files. Canonical
// settings, credentials and transcripts never enter the managed backup root.
func BackupDatabaseFamily(o BackupOptions) (BackupResult, error) {
	if !filepath.IsAbs(o.Scope) || !filepath.IsAbs(o.DatabasePath) || filepath.Clean(o.DatabasePath) != filepath.Join(filepath.Clean(o.Scope), "cc-haha", "db", filename(Primary)) {
		return BackupResult{}, unsafePath()
	}
	if o.Reason != "SQLITE_CORRUPT" && o.Reason != "MANUAL_REBUILD" {
		return BackupResult{}, errors.New("Invalid local index backup reason")
	}
	if e := PrepareManagedDatabasePath(o.DatabasePath, o.Scope, filename(Primary)); e != nil {
		return BackupResult{}, e
	}
	real, e := filepath.EvalSymlinks(o.Scope)
	if e != nil {
		return BackupResult{}, e
	}
	root := filepath.Join(filepath.Dir(o.DatabasePath), "backups")
	if e = directory(root, real); e != nil {
		return BackupResult{}, e
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Rename == nil {
		o.Rename = os.Rename
	}
	if o.Token == nil {
		o.Token = backupToken
	}
	token, e := o.Token()
	if e != nil {
		return BackupResult{}, e
	}
	if len(token) != 8 || strings.ContainsAny(token, "/\\\x00") {
		return BackupResult{}, unsafePath()
	}
	stamp := strings.NewReplacer(":", "-", ".", "-").Replace(o.Now().UTC().Format("2006-01-02T15:04:05.000Z"))
	pending := filepath.Join(root, ".pending-"+stamp+"-"+token)
	final := filepath.Join(root, stamp+"-"+strings.ToLower(o.Reason)+"-"+token)
	if e = os.Mkdir(pending, 0777); e != nil {
		return BackupResult{}, e
	}
	type movedFile struct{ source, destination string }
	moved := []movedFile{}
	for _, suffix := range []string{"", "-wal", "-shm"} {
		source := o.DatabasePath + suffix
		info, statErr := os.Lstat(source)
		if os.IsNotExist(statErr) {
			continue
		}
		if statErr != nil {
			e = statErr
			break
		}
		if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
			e = unsafePath()
			break
		}
		destination := filepath.Join(pending, filepath.Base(source))
		if e = o.Rename(source, destination); e != nil {
			break
		}
		moved = append(moved, movedFile{source, destination})
	}
	if e == nil && len(moved) > 0 {
		e = o.Rename(pending, final)
	}
	if e != nil {
		complete := true
		for n := len(moved) - 1; n >= 0; n-- {
			if o.Rename(moved[n].destination, moved[n].source) != nil {
				complete = false
			}
		}
		if complete {
			_ = os.RemoveAll(pending)
		}
		_, _ = enforceBackupRetention(root)
		return BackupResult{}, coded("LOCAL_INDEX_BACKUP_FAILED", "LOCAL_INDEX_BACKUP_FAILED")
	}
	if len(moved) == 0 {
		if e = os.RemoveAll(pending); e != nil {
			return BackupResult{}, e
		}
		final = root
	}
	removed, e := enforceBackupRetention(root)
	return BackupResult{final, len(moved), removed}, e
}
func backupDirectorySize(path string) (int64, error) {
	entries, e := os.ReadDir(path)
	if e != nil {
		return 0, e
	}
	var total int64
	for _, entry := range entries {
		if entry.Type()&os.ModeSymlink != 0 {
			continue
		}
		child := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			size, e := backupDirectorySize(child)
			if e != nil {
				return 0, e
			}
			total += size
		} else {
			info, e := entry.Info()
			if e != nil {
				return 0, e
			}
			if info.Mode().IsRegular() {
				total += info.Size()
			}
		}
	}
	return total, nil
}
func enforceBackupRetention(root string) (int, error) {
	entries, e := os.ReadDir(root)
	if os.IsNotExist(e) {
		return 0, nil
	}
	if e != nil {
		return 0, e
	}
	dirs := []os.DirEntry{}
	for _, entry := range entries {
		if entry.IsDir() && entry.Type()&os.ModeSymlink == 0 {
			dirs = append(dirs, entry)
		}
	}
	sort.Slice(dirs, func(i, j int) bool { return dirs[i].Name() > dirs[j].Name() })
	var retained int64
	removed := 0
	for n, entry := range dirs {
		path := filepath.Join(root, entry.Name())
		size, e := backupDirectorySize(path)
		if e != nil {
			return removed, e
		}
		retained += size
		if n < MaxBackups && retained <= MaxBackupBytes {
			continue
		}
		retained -= size
		if e = os.RemoveAll(path); e != nil {
			return removed, e
		}
		removed++
	}
	return removed, nil
}
