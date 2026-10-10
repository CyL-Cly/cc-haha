package sessioncontroller

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type drainCompletionFault struct{ operation, path string }

func (fault *drainCompletionFault) Error() string {
	return "fixture physical drain completion failure: " + fault.operation
}

type drainCompletionFiles struct {
	OSFiles
	fault  string
	armed  bool
	counts struct{ Open, Stat, Append, Close int }
}

func (files *drainCompletionFiles) OpenFile(path string, flags int, mode fs.FileMode) (TranscriptFile, error) {
	file, err := files.OSFiles.OpenFile(path, flags, mode)
	if err != nil {
		return nil, err
	}
	if !files.armed {
		return file, nil
	}
	files.counts.Open++
	return &drainCompletionFile{TranscriptFile: file, files: files, path: path}, nil
}

type drainCompletionFile struct {
	TranscriptFile
	files *drainCompletionFiles
	path  string
}

func (file *drainCompletionFile) Stat() (fs.FileInfo, error) {
	file.files.counts.Stat++
	info, err := file.TranscriptFile.Stat()
	if err == nil && strings.HasPrefix(file.files.fault, "stat") {
		err = &drainCompletionFault{"stat", file.path}
	}
	return info, err
}
func (file *drainCompletionFile) Write(bytes []byte) (int, error) {
	file.files.counts.Append++
	n, err := file.TranscriptFile.Write(bytes)
	if err == nil && strings.HasPrefix(file.files.fault, "append") {
		err = &drainCompletionFault{"appendFile", file.path}
	}
	return n, err
}
func (file *drainCompletionFile) Close() error {
	file.files.counts.Close++
	err := file.TranscriptFile.Close()
	if err == nil && strings.Contains(file.files.fault, "close") {
		err = &drainCompletionFault{"close", file.path}
	}
	return err
}

func TestTrackedDrainActualSourceFinallyAndPhysicalBytes(t *testing.T) {
	bytes, err := os.ReadFile("testdata/tracked_drain_source.json")
	if err != nil {
		t.Fatal(err)
	}
	var corpus struct {
		Cases []struct {
			Input struct {
				Fault string
				Queue json.RawMessage
			}
			Expected json.RawMessage
		}
	}
	if err := json.Unmarshal(bytes, &corpus); err != nil || len(corpus.Cases) != 6 {
		t.Fatalf("actual six original Source captures required: %v", err)
	}
	for _, mode := range []string{"pending", "sync"} {
		for _, row := range corpus.Cases {
			t.Run(mode+"/"+row.Input.Fault, func(t *testing.T) {
				home := t.TempDir()
				owner := cooperative.New()
				files := &drainCompletionFiles{fault: row.Input.Fault}
				writer := actualPendingWriter(t, owner, home, files)
				if err := os.MkdirAll(filepath.Dir(writer.target()), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(writer.target(), nil, 0600); err != nil {
					t.Fatal(err)
				}
				entry := &protocol.JSONObject{}
				if err := json.Unmarshal(row.Input.Queue, entry); err != nil {
					t.Fatal(err)
				}
				var pending *PendingWriter
				pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
					writer.options.Schedule = func(time.Duration, func()) func() { return func() {} }
					writer.AdoptSessionFile()
					var err error
					if mode == "pending" {
						pending, err = NewPendingWriter(writer, owner)
						if err != nil {
							return err
						}
					}
					// Queue acceptance/identity completes before the observed drain,
					// as in Source. Earlier native calls are outside this finite gate.
					if err = writer.Append(entry); err != nil {
						return err
					}
					files.armed = true
					return nil
				})
				t.Cleanup(func() {
					if pending != nil {
						_ = pending.Close(context.Background())
					}
					owner.Close()
				})
				var failure error
				if mode == "pending" {
					failure = awaitTrackedFlush(t, beginTrackedFlush(t, owner, pending))
				} else {
					pendingCall(t, owner, cooperative.Flush, func(*cooperative.Scope) error { failure = writer.Flush(); return nil })
				}
				var observedError any
				if failure != nil {
					var fault *drainCompletionFault
					if !errors.As(failure, &fault) {
						t.Fatal("unexpected native failure", failure)
					}
					code := "EIO"
					if fault.operation == "close" {
						code = "EBADF"
					}
					observedError = map[string]any{"name": "Error", "message": fault.Error(), "code": code, "syscall": fault.operation, "path": fault.path}
				}
				disk, err := os.ReadFile(writer.target())
				if err != nil {
					t.Fatal(err)
				}
				view := map[string]any{"fault": row.Input.Fault, "error": observedError, "counts": map[string]int{"open": files.counts.Open, "stat": files.counts.Stat, "append": files.counts.Append, "close": files.counts.Close}, "disk": string(disk)}
				actual, err := json.Marshal(view)
				if err != nil {
					t.Fatal(err)
				}
				actual = []byte(strings.ReplaceAll(strings.ReplaceAll(string(actual), sanitize(home), "<private-project>"), home, "<private-home>"))
				var got, want any
				if json.Unmarshal(actual, &got) != nil || json.Unmarshal(row.Expected, &want) != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("actual Source drain error/bytes/counts diverged\nactual=%s\nsource=%s", actual, row.Expected)
				}
			})
		}
	}

}

func TestSynchronousPrepareAndEnqueueRetainSourceFinallyAndIdentity(t *testing.T) {
	for _, operation := range []string{"prepare", "enqueue"} {
		for _, fault := range []string{"stat-close", "close"} {
			t.Run(operation+"/"+fault, func(t *testing.T) {
				owner := cooperative.New()
				t.Cleanup(owner.Close)
				files := &drainCompletionFiles{fault: fault}
				writer := actualPendingWriter(t, owner, t.TempDir(), files)
				if err := os.MkdirAll(filepath.Dir(writer.target()), 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(writer.target(), nil, 0600); err != nil {
					t.Fatal(err)
				}
				pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
					writer.options.Schedule = func(time.Duration, func()) func() { return func() {} }
					writer.AdoptSessionFile()
					files.armed = true
					var err error
					if operation == "prepare" {
						err = writer.prepare(writer.target())
					} else {
						err = writer.Append(plainMessage("unknown-identity", "actual native identity"))
					}
					var external *drainCompletionFault
					if !errors.As(err, &external) || external.operation != "close" {
						return errors.New("synchronous finally close priority differs from Source")
					}
					_, installed := writer.identities[writer.target()]
					if installed != (fault == "close") || files.counts.Close != 1 {
						return errors.New("identity installation/real close changed across failing finally")
					}
					return nil
				})
			})
		}
	}
}
