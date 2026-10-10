package diagnosticsapi

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

type scanResult struct {
	Events                        []Event
	Physical, Corrupt, RawCorrupt int
	Source                        []byte
}
type corruptionEvidence struct {
	Corrupt int    `json:"corruptLineCount"`
	Bytes   int    `json:"sourceBytes"`
	Digest  string `json:"sourceDigest"`
}

func digest(b []byte) string { hash := sha256.Sum256(b); return hex.EncodeToString(hash[:]) }
func parseDate(value string) (time.Time, error) {
	milliseconds, valid, err := sessionprojection.ParseDateUTC(utf16.Encode([]rune(value)))
	if err != nil {
		return time.Time{}, err
	}
	if !valid {
		return time.Time{}, &time.ParseError{Layout: "Date.parse", Value: value}
	}
	return time.UnixMilli(int64(milliseconds)), nil
}
func (s *Service) scan() (scanResult, error) {
	result := scanResult{Events: []Event{}}
	raw, err := os.ReadFile(s.DiagnosticsPath())
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	result.Source = raw
	lines := bytes.Split(raw, []byte{'\n'})
	if len(raw) == 0 {
		lines = nil
	} else if raw[len(raw)-1] == '\n' {
		lines = lines[:len(lines)-1]
	}
	result.Physical = len(lines)
	for _, line := range lines {
		var event Event
		var fields map[string]json.RawMessage
		if json.Unmarshal(line, &fields) != nil || fields == nil || json.Unmarshal(line, &event) != nil {
			result.RawCorrupt++
			continue
		}
		_, dateErr := parseDate(event.Timestamp)
		isString := func(name string) bool { raw := bytes.TrimSpace(fields[name]); return len(raw) > 0 && raw[0] == '"' }
		id := isString("id")
		typ := isString("type")
		summary := isString("summary")
		valid := id && typ && summary && dateErr == nil && IsSeverity(event.Severity)
		if _, exists := fields["sessionId"]; exists && !isString("sessionId") {
			valid = false
		}
		if !valid {
			result.RawCorrupt++
			continue
		}
		result.Events = append(result.Events, event)
	}
	var committed corruptionEvidence
	b, err := os.ReadFile(filepath.Join(s.LogDir(), "corruption-evidence.json"))
	if err != nil && !os.IsNotExist(err) {
		return result, err
	}
	_ = json.Unmarshal(b, &committed)
	if committed.Corrupt < 0 {
		committed.Corrupt = 0
	}
	var pending corruptionEvidence
	p, _ := os.ReadFile(filepath.Join(s.LogDir(), "corruption-evidence.pending.json"))
	if json.Unmarshal(p, &pending) == nil && pending.Corrupt >= 0 && pending.Bytes >= 0 && pending.Digest != "" {
		present := len(raw) >= pending.Bytes && digest(raw[:pending.Bytes]) == pending.Digest
		if !(result.RawCorrupt > 0 && present) && pending.Corrupt > committed.Corrupt {
			committed.Corrupt = pending.Corrupt
		}
	}
	result.Corrupt = committed.Corrupt + result.RawCorrupt
	return result, nil
}
func (s *Service) retention(force, compact bool) error {
	now := s.options.Now()
	cutoff := now.Add(-retentionDays * 24 * time.Hour)
	info, err := os.Stat(s.DiagnosticsPath())
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	oversized := err == nil && info.Size() > maxDiagnosticsBytes
	if !force && !oversized && !s.lastSweep.IsZero() && now.Sub(s.lastSweep) < time.Minute {
		return nil
	}
	if info != nil {
		scan, err := s.scan()
		if err != nil {
			return err
		}
		stale := false
		for _, e := range scan.Events {
			t, _ := parseDate(e.Timestamp)
			if t.Before(cutoff) {
				stale = true
				break
			}
		}
		if compact || oversized || stale {
			if scan.RawCorrupt > 0 {
				evidence := corruptionEvidence{scan.Corrupt, len(scan.Source), digest(scan.Source)}
				b, _ := json.Marshal(evidence)
				if err = s.writeFile(filepath.Join(s.LogDir(), "corruption-evidence.pending.json"), append(b, '\n'), false); err != nil {
					return err
				}
			}
			retained := []Event{}
			for _, e := range scan.Events {
				t, _ := parseDate(e.Timestamp)
				if !t.Before(cutoff) {
					retained = append(retained, e)
				}
			}
			sort.SliceStable(retained, func(i, j int) bool {
				left, _ := parseDate(retained[i].Timestamp)
				right, _ := parseDate(retained[j].Timestamp)
				return left.Before(right)
			})
			lines := [][]byte{}
			var total int64
			for i := len(retained) - 1; i >= 0; i-- {
				b, _ := json.Marshal(retained[i])
				if int64(len(b)+1) > maxDiagnosticsBytes-total {
					break
				}
				lines = append(lines, append(b, '\n'))
				total += int64(len(b) + 1)
			}
			var content bytes.Buffer
			for i := len(lines) - 1; i >= 0; i-- {
				content.Write(lines[i])
			}
			if err = s.atomicWrite(s.DiagnosticsPath(), content.Bytes()); err != nil {
				return err
			}
			if scan.RawCorrupt > 0 {
				if err = os.Rename(filepath.Join(s.LogDir(), "corruption-evidence.pending.json"), filepath.Join(s.LogDir(), "corruption-evidence.json")); err != nil {
					return err
				}
			}
		}
	}
	for _, path := range []string{s.RuntimeErrorsPath(), s.ElectronHostPath()} {
		if err = s.truncateTail(path, maxAuxiliaryBytes); err != nil {
			return err
		}
	}
	if err = s.cliRetention(cutoff); err != nil {
		return err
	}
	if err = s.exportRetention(cutoff); err != nil {
		return err
	}
	s.lastSweep = now
	return nil
}
func (s *Service) truncateTail(path string, max int64) error {
	file, err := s.openFile(path, os.O_RDONLY)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		file.Close()
		return err
	}
	if info.Size() <= max {
		return file.Close()
	}
	size := min(info.Size(), max+4096)
	tail := make([]byte, size)
	_, err = file.ReadAt(tail, info.Size()-size)
	closeErr := file.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if index := bytes.IndexByte(tail, '\n'); index >= 0 {
		tail = tail[index+1:]
	} else if int64(len(tail)) > max {
		tail = tail[len(tail)-int(max):]
	}
	for int64(len(tail)) > max {
		tail = tail[1:]
	}
	return s.atomicWrite(path, tail)
}
func isCli(name string) bool {
	return name == "cli-diagnostics.jsonl" || strings.HasPrefix(name, "cli-diagnostics.jsonl.") && strings.HasSuffix(name, ".jsonl")
}
func (s *Service) exportRetention(cutoff time.Time) error {
	files, err := s.listFiles(s.ExportDir())
	if err != nil {
		return err
	}
	sort.SliceStable(files, func(i, j int) bool { return files[i].ModTime.Before(files[j].ModTime) })
	var total int64
	for _, f := range files {
		total += f.Size
	}
	for _, f := range files {
		if !f.ModTime.Before(cutoff) && total <= maxExportBytes {
			continue
		}
		if err = os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= f.Size
	}
	return nil
}

var currentPid = regexp.MustCompile(`^cli-diagnostics\.jsonl\.(\d+)\.current\.jsonl$`)

func (s *Service) cliRetention(cutoff time.Time) error {
	files, err := s.listFiles(s.LogDir())
	if err != nil {
		return err
	}
	removed := map[string]bool{}
	var completed []fileMetadata
	var total int64
	for _, f := range files {
		if filepath.Dir(f.Path) != s.LogDir() || !isCli(filepath.Base(f.Path)) {
			continue
		}
		name := filepath.Base(f.Path)
		if strings.Contains(name, ".reclaimed.") {
			if f.ModTime.Before(cutoff) {
				if err = os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
					return err
				}
			}
			continue
		}
		quarantine := f.ModTime.Before(cutoff) && (name == "cli-diagnostics.jsonl" || strings.HasSuffix(name, ".current.jsonl"))
		if match := currentPid.FindStringSubmatch(name); match != nil {
			pid, _ := strconv.Atoi(match[1])
			quarantine = quarantine || pidLiveness(pid) == "dead"
		}
		if quarantine {
			id, err := identifier()
			if err != nil {
				return err
			}
			stem := strings.TrimSuffix(f.Path, ".current.jsonl")
			if err = os.Rename(f.Path, stem+".reclaimed."+id+".jsonl"); err != nil && !os.IsNotExist(err) {
				return err
			}
			removed[f.Path] = true
			continue
		}
		if name == "cli-diagnostics.jsonl" {
			total += f.Size
		} else if !strings.HasSuffix(name, ".current.jsonl") {
			completed = append(completed, f)
			total += f.Size
		}
	}
	sort.SliceStable(completed, func(i, j int) bool { return completed[i].ModTime.Before(completed[j].ModTime) })
	for _, f := range completed {
		if !f.ModTime.Before(cutoff) && total <= maxCliBytes {
			continue
		}
		if err = os.Remove(f.Path); err != nil && !os.IsNotExist(err) {
			return err
		}
		total -= f.Size
	}
	return nil
}
func (s *Service) textTail(path string, max int) (string, error) {
	file, err := s.openFile(path, os.O_RDONLY)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() <= int64(max) {
		b, err := readAll(file)
		return s.SanitizeString(string(b), max), err
	}
	size := min(info.Size(), int64(max+4096))
	b := make([]byte, size)
	if _, err = file.ReadAt(b, info.Size()-size); err != nil {
		return "", err
	}
	if info.Size() > size {
		if index := bytes.IndexByte(b, '\n'); index >= 0 {
			b = b[index+1:]
		} else {
			b = nil
		}
	}
	value := s.SanitizeString(string(b), -1)
	units := []rune(value)
	if len(units) > max {
		value = string(units[len(units)-max:])
	}
	return truncatedOlder + value, nil
}
func (s *Service) readCli() (string, error) {
	seen := map[string]bool{}
	contents := map[string]bool{}
	var chunks strings.Builder
	for attempt := 0; attempt < 3; attempt++ {
		files, err := s.listFiles(s.LogDir())
		if err != nil {
			return "", err
		}
		sort.SliceStable(files, func(i, j int) bool { return files[i].ModTime.Before(files[j].ModTime) })
		for _, file := range files {
			if filepath.Dir(file.Path) != s.LogDir() || !isCli(filepath.Base(file.Path)) || seen[file.Path] {
				continue
			}
			content, err := s.textTail(file.Path, textExportLength)
			if err != nil {
				return "", err
			}
			if content == "" {
				continue
			}
			seen[file.Path] = true
			hash := digest([]byte(content))
			if contents[hash] {
				continue
			}
			contents[hash] = true
			chunks.WriteString(content)
		}
	}
	combined := chunks.String()
	units := []rune(combined)
	if len(units) > textExportLength {
		return truncatedOlder + string(units[len(units)-textExportLength:]), nil
	}
	return combined, nil
}
