package sessionlocator

import (
	"bytes"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/sessionhistory"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
)

type measuredFiles struct {
	content              map[string][]byte
	opens, bytes, closes int
	closeError           error
}
type measuredFile struct {
	files *measuredFiles
	data  []byte
}

func (files *measuredFiles) Open(path string) (sessionstore.File, error) {
	files.opens++
	return &measuredFile{files: files, data: files.content[path]}, nil
}
func (file *measuredFile) ReadAt(out []byte, offset int64) (int, error) {
	n, err := bytes.NewReader(file.data).ReadAt(out, offset)
	file.files.bytes += n
	return n, err
}
func (file *measuredFile) Stat() (sessionstore.Snapshot, error) {
	return sessionstore.Snapshot{Dev: "1", Ino: "2", MtimeNS: "3", Size: int64(len(file.data))}, nil
}
func (file *measuredFile) Close() error { file.files.closes++; return file.files.closeError }

func measuredService(t *testing.T, directories []string, files *measuredFiles) *Service {
	t.Helper()
	budget := sessionstore.NewBudget()
	service, err := New(Options{ConfigDir: func() string { return "/fixture" }, ReadDir: func(string) ([]string, error) { return directories, nil }, Stat: func(string) (float64, error) { return 1, nil }, Reader: sessionstore.NewReaderWithBudget(files, budget), Budget: budget, Compare: sessionhistory.CompareStrings})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestSingleCandidateLookupDoesNotOpenTranscript(t *testing.T) {
	files := &measuredFiles{}
	result, err := measuredService(t, []string{"project"}, files).FindFiles(fixtureID)
	if err != nil || len(result) != 1 || files.opens != 0 {
		t.Fatal(result, err, files.opens)
	}
}

func TestCandidateRankingStopsAtFirstConversationAndCloses(t *testing.T) {
	record := "{\"type\":\"user\",\"message\":{\"role\":\"user\",\"content\":\"question\"}}\n"
	files := &measuredFiles{content: map[string][]byte{
		"/fixture/projects/a/" + fixtureID + ".jsonl": []byte(record + strings.Repeat("{}\n", 2_000_000)),
		"/fixture/projects/b/" + fixtureID + ".jsonl": []byte("{\"type\":\"session-meta\"}\n"),
	}}
	result, err := measuredService(t, []string{"a", "b"}, files).FindFiles(fixtureID)
	if err != nil || len(result) != 2 || result[0].ProjectDir != "a" {
		t.Fatal(result, err)
	}
	if files.bytes > 128*1024 || files.opens != 2 || files.closes != 2 {
		t.Fatalf("unbounded lookup or leaked file: %+v", files)
	}
}

func TestEarlyDiscoveryStillHonorsCloseFailure(t *testing.T) {
	files := &measuredFiles{content: map[string][]byte{}, closeError: io.ErrUnexpectedEOF}
	service := measuredService(t, []string{"a", "b"}, files)
	files.content["p"] = []byte("{\"type\":\"user\",\"message\":{\"role\":\"user\"}}\n")
	_, err := service.hasTranscript("p")
	if !errors.Is(err, io.ErrUnexpectedEOF) || files.closes != 1 {
		t.Fatal(err, files.closes)
	}
}
