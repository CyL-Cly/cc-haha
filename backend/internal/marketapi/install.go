package marketapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

func installFailure(err error) error {
	var e *Error
	if errors.As(err, &e) {
		return &Error{502, e.Code, e.Message}
	}
	return fail(502, "MARKET_UPSTREAM_ERROR", "Upstream download failed: "+err.Error())
}
func (s *Service) Install(ctx context.Context, source, slug string, owner ...string) (object, error) {
	ctx, _ = ownerContext(ctx, source, slug, owner)
	s.mu.Lock()
	if s.inFlight[slug] {
		s.mu.Unlock()
		return nil, fail(409, "MARKET_INSTALL_IN_PROGRESS", "Install already in progress for "+slug)
	}
	s.inFlight[slug] = true
	s.mu.Unlock()
	defer func() { s.mu.Lock(); delete(s.inFlight, slug); s.mu.Unlock() }()
	dir := safeDir(slug)
	if dir == "" {
		return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Skill slug cannot be used as a directory name: "+slug)
	}
	detail, _, err := s.Detail(ctx, source, slug, owner...)
	if err != nil {
		return nil, installFailure(err)
	}
	if detail["installState"] == "installed" {
		return nil, fail(409, "MARKET_ALREADY_INSTALLED", "Skill already installed: "+slug)
	}
	if detail["installState"] == "not-installable" {
		return nil, fail(422, "MARKET_NOT_INSTALLABLE", fmt.Sprintf("Skill is not installable (%s): %s", detail["notInstallableReason"], slug))
	}
	files, err := s.listFiles(ctx, source, slug, str(detail["version"]))
	if err != nil {
		return nil, installFailure(err)
	}
	hasSkill := false
	total := float64(0)
	large := false
	for _, f := range files {
		hasSkill = hasSkill || f["path"] == "SKILL.md"
		total += num(f["size"])
		large = large || num(f["size"]) > maxFileSize
	}
	if len(files) == 0 || !hasSkill {
		return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Skill has no installable files: "+slug)
	}
	if len(files) > maxFileCount {
		return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Skill has too many files: "+slug)
	}
	if large || total > maxTotalSize {
		return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Skill files exceed the size limit: "+slug)
	}
	tmp, err := os.MkdirTemp("", "haha-market-install-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(tmp)
	actual := 0
	for _, file := range files {
		path := str(file["path"])
		clean := filepath.Clean(path)
		if path == "" || len(utf16.Encode([]rune(path))) > 512 || filepath.IsAbs(path) || strings.HasPrefix(clean, "..") || strings.Contains(clean, ".."+string(filepath.Separator)) || strings.ContainsRune(path, 0) {
			return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Unsafe file path in skill: "+path)
		}
		content, size, err := s.fetchFile(ctx, source, slug, path)
		if err != nil {
			return nil, installFailure(err)
		}
		if size > maxFileSize {
			return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Skill file exceeds the actual size limit: "+path)
		}
		actual += size
		if actual > maxTotalSize {
			return nil, fail(422, "MARKET_NOT_INSTALLABLE", "Skill files exceed the actual total size limit: "+slug)
		}
		if checksum := str(file["sha256"]); checksum != "" {
			sum := sha256.Sum256([]byte(content))
			if hex.EncodeToString(sum[:]) != strings.ToLower(checksum) {
				return nil, fail(502, "MARKET_CHECKSUM_MISMATCH", "Checksum mismatch for "+path+" — aborting install")
			}
		}
		target := filepath.Join(tmp, path)
		if err = os.MkdirAll(filepath.Dir(target), 0755); err != nil {
			return nil, err
		}
		if err = os.WriteFile(target, []byte(content), 0644); err != nil {
			return nil, err
		}
	}
	m := object{"id": source + ":" + slug, "source": source, "slug": slug, "installedAt": s.o.Now().UTC().Format("2006-01-02T15:04:05.000Z"), "fileCount": len(files)}
	optional(m, "version", detail["version"])
	ordered := protocol.NewJSONObject()
	for _, key := range []string{"id", "source", "slug", "version", "installedAt", "fileCount"} {
		if value, ok := m[key]; ok {
			raw, _ := json.Marshal(value)
			ordered.Set(key, raw)
		}
	}
	data, _ := json.MarshalIndent(ordered, "", "  ")
	if err = os.WriteFile(filepath.Join(tmp, metaFilename), append(data, '\n'), 0644); err != nil {
		return nil, err
	}
	if err = os.MkdirAll(s.skillsDir(), 0755); err != nil {
		return nil, fail(500, "MARKET_DISK_ERROR", "Cannot create skills directory: "+err.Error())
	}
	target := filepath.Join(s.skillsDir(), dir)
	if _, err = os.Stat(target); err == nil {
		return nil, fail(409, "MARKET_ALREADY_INSTALLED", "Skill directory already exists: "+dir)
	}
	if err = os.Rename(tmp, target); errors.Is(err, syscall.EXDEV) {
		err = copyTree(tmp, target)
	}
	if err != nil {
		return nil, fail(500, "MARKET_DISK_ERROR", "Failed to write skill to disk: "+err.Error())
	}
	if s.o.Changed != nil {
		s.o.Changed()
	}
	return object{"ok": true, "installedPath": target, "skill": s.annotate(detail)}, nil
}
func copyTree(source, target string) error {
	return filepath.WalkDir(source, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		dest := filepath.Join(target, rel)
		if entry.IsDir() {
			return os.MkdirAll(dest, 0755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(dest, data, 0644)
	})
}
func (s *Service) Uninstall(ctx context.Context, source, slug string) (object, error) {
	dir := safeDir(slug)
	if dir == "" {
		return nil, fail(400, "BAD_REQUEST", "Invalid skill slug: "+slug)
	}
	target := filepath.Join(s.skillsDir(), dir)
	info, err := os.Lstat(target)
	if err != nil {
		return nil, fail(404, "MARKET_NOT_INSTALLED", "Skill is not installed: "+slug)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, fail(409, "MARKET_NOT_MANAGED", "Skill directory is not a managed directory: "+dir)
	}
	m := s.meta(dir)
	if m == nil || m["source"] != source || m["slug"] != slug || m["id"] != source+":"+slug {
		return nil, fail(409, "MARKET_NOT_MANAGED", "Skill directory was not installed from the market: "+dir)
	}
	if err = os.RemoveAll(target); err != nil {
		return nil, err
	}
	if s.o.Changed != nil {
		s.o.Changed()
	}
	var skill any
	detail, _, err := s.Detail(ctx, source, slug)
	if err == nil {
		skill = detail
	}
	return object{"skill": skill, "removedPath": target, "ok": true}, nil
}
