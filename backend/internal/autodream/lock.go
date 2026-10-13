package autodream

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

var progressRecord = regexp.MustCompile(`^([0-9]+) auto-dream-v1 ([0-9]+(?:\.[0-9]+)?)$`)
var prefixInteger = regexp.MustCompile(`^[+-]?[0-9]+`)

func parseRecord(raw string) (int, float64, bool) {
	match := progressRecord.FindStringSubmatch(protocol.TrimJavaScriptString(raw))
	if match == nil {
		return 0, 0, false
	}
	pid, err := strconv.ParseFloat(match[1], 64)
	prior, e := strconv.ParseFloat(match[2], 64)
	if err != nil || e != nil || pid <= 1 || pid > 9007199254740991 || math.IsInf(prior, 0) || math.IsNaN(prior) {
		return 0, 0, false
	}
	return int(pid), prior, true
}
func (s *Service) lockPath() (string, error) {
	root, err := s.memoryRoot()
	return filepath.Join(root, ".consolidate-lock"), err
}
func stamp(time time.Time) float64 { return float64(time.UnixNano()) / 1e6 }
func (s *Service) ReadLastConsolidatedAt() float64 {
	path, err := s.lockPath()
	if err != nil {
		return 0
	}
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	pid, prior, ok := parseRecord(string(raw))
	if ok && !s.options.IsProcessRunning(pid) {
		return prior
	}
	return stamp(info.ModTime())
}
func (s *Service) TryAcquire() (*float64, error) {
	path, err := s.lockPath()
	if err != nil {
		return nil, err
	}
	prior := float64(0)
	info, statErr := os.Stat(path)
	raw, readErr := os.ReadFile(path)
	if statErr == nil && readErr == nil {
		prior = stamp(info.ModTime())
		if _, old, ok := parseRecord(string(raw)); ok {
			prior = old
		}
		parsed := prefixInteger.FindString(protocol.TrimJavaScriptString(string(raw)))
		holder, err := strconv.ParseFloat(parsed, 64)
		if s.options.NowMillis()-stamp(info.ModTime()) < 3600000 && err == nil && holder <= float64(math.MaxInt) && holder >= float64(math.MinInt) && s.options.IsProcessRunning(int(holder)) {
			return nil, nil
		}
	}
	if err = os.MkdirAll(filepath.Dir(path), 0777); err != nil {
		return nil, err
	}
	priorJSON := []byte(strconv.FormatFloat(prior, 'g', -1, 64))
	quoted, _ := protocol.JavaScriptStringJSON(priorJSON)
	var priorString string
	_ = json.Unmarshal(quoted, &priorString)
	record := strconv.Itoa(s.options.PID) + " auto-dream-v1 " + priorString
	if err = os.WriteFile(path, []byte(record), 0666); err != nil {
		return nil, err
	}
	verify, err := os.ReadFile(path)
	if err != nil || string(verify) != record {
		return nil, nil
	}
	return &prior, nil
}
func (s *Service) Complete() {
	path, err := s.lockPath()
	if err != nil {
		s.failure(err)
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		s.failure(err)
		return
	}
	atime := accessTime(path, info)
	raw, err := os.ReadFile(path)
	if err != nil {
		s.failure(err)
		return
	}
	pid, _, ok := parseRecord(string(raw))
	if !ok || pid != s.options.PID {
		return
	}
	if err = os.WriteFile(path, nil, 0666); err == nil {
		err = os.Chtimes(path, atime, info.ModTime())
	}
	s.failure(err)
}
func (s *Service) Rollback(prior float64) {
	path, err := s.lockPath()
	if err != nil {
		s.failure(err)
		return
	}
	if prior == 0 {
		s.failure(os.Remove(path))
		return
	}
	if err = os.WriteFile(path, nil, 0666); err == nil {
		at := time.Unix(0, int64(prior*1e6))
		err = os.Chtimes(path, at, at)
	}
	s.failure(err)
}
func (s *Service) Record() {
	path, err := s.lockPath()
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), 0777)
	}
	if err == nil {
		err = os.WriteFile(path, nil, 0666)
	}
	s.failure(err)
}

// Source parseInt permits a numeric prefix even when a legacy body is not a
// versioned attempt. Its timestamp remains authoritative after process death.
func LegacyPID(raw string) (int, bool) {
	prefix := prefixInteger.FindString(strings.TrimLeftFunc(raw, protocol.IsJavaScriptWhitespace))
	n, err := strconv.Atoi(prefix)
	return n, err == nil
}
