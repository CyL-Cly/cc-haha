package cliglobalconfig

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"math/big"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func (service *Service) backup() {
	if err := service.makeBackup(); err != nil && !errors.Is(err, fs.ErrNotExist) {
		service.emit("backup_failed", map[string]any{"error": err.Error()})
	}
}
func (service *Service) makeBackup() error {
	directory := filepath.Join(service.configHome, "backups")
	if err := service.options.FS.MkdirAll(directory); err != nil && !errors.Is(err, fs.ErrExist) {
		return err
	}
	prefix := filepath.Base(service.Path()) + ".backup."
	list := func() ([]string, error) {
		entries, err := service.options.FS.ReadDir(directory)
		if err != nil {
			return nil, err
		}
		names := []string{}
		for _, name := range entries {
			if strings.HasPrefix(name, prefix) {
				names = append(names, name)
			}
		}
		sort.Slice(names, func(i, j int) bool { return lessUTF16(names[j], names[i]) })
		return names, nil
	}
	entries, err := list()
	if err != nil {
		return err
	}
	timestamp := float64(0)
	if len(entries) > 0 {
		parts := strings.Split(entries[0], ".backup.")
		timestamp = jsNumber(parts[len(parts)-1])
	}
	create := math.IsNaN(timestamp) || float64(service.options.Now().UnixMilli())-timestamp >= 60000
	if create {
		next := filepath.Join(directory, fmt.Sprintf("%s%d", prefix, service.options.Now().UnixMilli()))
		if err := service.options.FS.CopyFile(service.Path(), next); err != nil {
			return err
		}
		entries, err = list()
		if err != nil {
			return err
		}
	}
	if len(entries) > 5 {
		for _, name := range entries[5:] {
			_ = service.options.FS.Remove(filepath.Join(directory, name))
		}
	}
	return nil
}
func jsNumber(value string) float64 {
	value = strings.TrimFunc(value, func(r rune) bool {
		return r >= 9 && r <= 13 || r == 32 || r == 160 || r == 0x1680 || r >= 0x2000 && r <= 0x200a || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000 || r == 0xfeff
	})
	if value == "" {
		return 0
	}
	if value == "Infinity" || value == "+Infinity" {
		return math.Inf(1)
	}
	if value == "-Infinity" {
		return math.Inf(-1)
	}
	if len(value) > 2 && value[0] == '0' {
		base := 0
		switch value[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			text := value[2:]
			if strings.ContainsAny(text, "+-_") {
				return math.NaN()
			}
			number, valid := new(big.Int).SetString(text, base)
			if !valid {
				return math.NaN()
			}
			parsed, _ := new(big.Float).SetInt(number).Float64()
			return parsed
		}
	}
	if !jsonNumberPattern.MatchString(value) {
		return math.NaN()
	}
	number, err := strconv.ParseFloat(value, 64)
	if err != nil && !errors.Is(err, strconv.ErrRange) {
		return math.NaN()
	}
	return number
}

var jsonNumberPattern = regexp.MustCompile(`^[+-]?(?:[0-9]+\.?[0-9]*|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)

func (service *Service) writeFile(path string, content []byte, newMode *fs.FileMode) error {
	target := path
	if link, err := service.options.FS.Readlink(path); err == nil {
		target = link
		if !filepath.IsAbs(link) {
			target = filepath.Join(filepath.Dir(path), link)
		}
	}
	temporary := fmt.Sprintf("%s.tmp.%d.%d", target, service.options.PID, service.options.Now().UnixMilli())
	stat, err := service.options.FS.Stat(target)
	exists := err == nil
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	var initialMode *fs.FileMode
	if !exists {
		initialMode = newMode
	}
	err = service.options.FS.WriteFlush(temporary, content, initialMode)
	if err == nil && exists {
		err = service.options.FS.Chmod(temporary, stat.Mode)
	}
	if err == nil {
		err = service.options.FS.Rename(temporary, target)
	}
	if err == nil {
		return nil
	}
	service.emit("atomic_write_error", map[string]any{"error": err.Error()})
	_ = service.options.FS.Remove(temporary)
	return service.options.FS.WriteFlush(target, content, initialMode)
}
