package sessioncontroller

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliglobalconfig"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/streamhistory"
)

type pendingContract struct {
	Cases []struct {
		Mode  string
		Input struct {
			UserSource, Prefix, Tail, RequestID string
			Now                                 int64
			Files                               map[string]string
		}
		Expected struct{ Files map[string]string }
	}
}
type heldFiles struct {
	OSFiles
	mode          string
	arm           chan struct{}
	held, release chan struct{}
	once          sync.Once
	closed        chan struct{}
	closeOnce     sync.Once
}

func (files *heldFiles) pause(kind string) {
	select {
	case <-files.arm:
	default:
		return
	}
	if kind == files.mode {
		files.once.Do(func() { close(files.held); <-files.release })
	}
}
func (files *heldFiles) OpenFile(path string, flags int, mode fs.FileMode) (TranscriptFile, error) {
	if flags&os.O_CREATE == 0 {
		files.pause("open")
	}
	file, err := files.OSFiles.OpenFile(path, flags, mode)
	if err != nil {
		return nil, err
	}
	return &heldFile{TranscriptFile: file, files: files, natural: flags&os.O_CREATE == 0}, nil
}

type heldFile struct {
	TranscriptFile
	files   *heldFiles
	natural bool
}

func (file *heldFile) Stat() (fs.FileInfo, error) {
	if file.natural {
		file.files.pause("stat")
	}
	return file.TranscriptFile.Stat()
}
func (file *heldFile) Close() error {
	err := file.TranscriptFile.Close()
	if file.natural && file.files.closed != nil {
		file.files.closeOnce.Do(func() { close(file.files.closed) })
	}
	return err
}
func pendingEvents(wire string) []json.RawMessage {
	events := []json.RawMessage{}
	for _, line := range strings.Split(wire, "\n") {
		if strings.HasPrefix(line, "data: ") {
			events = append(events, json.RawMessage(strings.TrimPrefix(line, "data: ")))
		}
	}
	return events
}
func pendingCall(t *testing.T, owner *cooperative.Owner, class cooperative.Class, fn func(*cooperative.Scope) error) {
	t.Helper()
	receipt, err := owner.Submit(context.Background(), class, fn)
	if err == nil {
		err = receipt.Wait()
	}
	if err != nil {
		t.Fatal(err)
	}
}
func awaitSignal(t *testing.T, signal <-chan struct{}, description string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out: " + description)
	}
}
func actualPendingWriter(t *testing.T, owner *cooperative.Owner, home string, files TranscriptFiles) *Writer {
	t.Helper()
	var writer *Writer
	pendingCall(t, owner, cooperative.Input, func(*cooperative.Scope) error {
		now := func() time.Time { return time.UnixMilli(1700000000000) }
		state, err := clisettings.NewState(func() float64 { return float64(now().UnixMilli()) })
		if err != nil {
			return err
		}
		config := filepath.Join(home, ".claude")
		if err = os.MkdirAll(config, 0700); err != nil {
			return err
		}

		service, err := clisettings.NewService(clisettings.Options{State: state, ConfigHome: config, OriginalCWD: home, Profile: clisettings.ExternalReleaseProfile, PID: 1, Now: func() float64 { return float64(now().UnixMilli()) }})
		if err != nil {
			return err
		}
		global, err := cliglobalconfig.New(cliglobalconfig.Options{Home: home, ConfigDir: &config, Now: now, PID: 1, Stderr: func(value string) { t.Log(value) }})
		if err != nil {
			return err
		}
		t.Cleanup(func() {
			if err := global.Close(); err != nil {
				t.Error(err)
			}
		})
		if err = global.Enable(); err != nil {
			return err
		}
		environment, err := clistartupenv.NewEnvironment(protocol.NewJSONObject(protocol.JSONField{Name: "HOME", Value: quote(home)}, protocol.JSONField{Name: "CLAUDE_CONFIG_DIR", Value: quote(config)}, protocol.JSONField{Name: "USER_TYPE", Value: quote("external")}, protocol.JSONField{Name: "CLAUDE_CODE_SIMPLE", Value: quote("1")}))
		if err != nil {
			return err
		}
		resolver, err := clistartupenv.New(clistartupenv.Options{Global: global, Settings: service, SettingsState: state, Environment: environment, ConfigHome: config, Host: clistartupenv.Host{Profile: clistartupenv.EmptyPolicyReleaseProfile, RemoteCacheAbsent: true, MDMCacheAbsent: true, HKCUCacheAbsent: true, PolicyFilesAbsent: true, PluginBaseAbsent: true}})
		if err != nil {
			return err
		}
		if _, err = resolver.GetInitialSettings(); err != nil {
			return err
		}
		writer, err = NewWriter(WriterOptions{ConfigDir: config, CWD: home, SessionID: "00000000-0000-4000-8000-000000000000", Version: "fixture-version", Environment: map[string]string{"NODE_ENV": "production"}, SettingsState: state, Files: files, Branch: func() (string, error) {
			return NoRepositoryBranch(home, func(path string) (os.FileInfo, error) {
				if path == filepath.Join(home, ".git") {
					return os.Stat(path)
				}
				return nil, os.ErrNotExist
			})
		}, OnError: func(e error) { t.Errorf("natural writer: %v", e) }, Schedule: RealSchedule, Now: now})
		return err
	})
	return writer
}
func TestPendingWriterActualSourceOpenStatContracts(t *testing.T) {
	data, err := os.ReadFile("testdata/pending_source_contract.json")
	if err != nil {
		t.Fatal(err)
	}
	var contract pendingContract
	if err = json.Unmarshal(data, &contract); err != nil || len(contract.Cases) != 2 {
		t.Fatalf("capture: %v", err)
	}
	for _, record := range contract.Cases {
		t.Run(record.Mode, func(t *testing.T) {
			home := t.TempDir()
			for path, data := range record.Input.Files {
				path = strings.ReplaceAll(path, "<private-project-key>", sanitize(home))
				target := filepath.Join(home, path)
				if err = os.MkdirAll(filepath.Dir(target), 0700); err != nil {
					t.Fatal(err)
				}
				if err = os.WriteFile(target, []byte(strings.ReplaceAll(data, "<private-home>", home)), 0600); err != nil {
					t.Fatal(err)
				}
			}
			owner := cooperative.New()
			defer owner.Close()
			files := &heldFiles{mode: record.Mode, arm: make(chan struct{}), held: make(chan struct{}), release: make(chan struct{}), closed: make(chan struct{})}
			writer := actualPendingWriter(t, owner, home, files)
			var pending *PendingWriter
			var controller *streamhistory.Controller
			var user protocol.JSONObject
			if err = json.Unmarshal([]byte(record.Input.UserSource), &user); err != nil {
				t.Fatal(err)
			}
			history := []LiveRecord{{Record: &user}}
			assistants := []*streamhistory.Envelope{}
			pendingCall(t, owner, cooperative.Input, func(scope *cooperative.Scope) error {
				_, err := writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, history, nil)
				if err != nil {
					return err
				}
				if err = writer.Flush(); err != nil {
					return err
				}
				counter := 0
				controller, err = streamhistory.New(streamhistory.Options{UUID: func() string { counter++; return fmt.Sprintf("fixture-id-%d", counter) }, Now: func() time.Time { return time.UnixMilli(record.Input.Now) }, RequestID: &record.Input.RequestID})
				if err != nil {
					return err
				}
				pending, err = NewPendingWriter(writer, owner)
				return err
			})
			tailReady, modelComplete := make(chan struct{}), make(chan struct{})
			done := make(chan error, 1)
			var checkpoint *WriteFuture
			go func() {
				done <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					emit := func(update streamhistory.Update) error {
						if update.Envelope == nil {
							return nil
						}
						assistants = append(assistants, update.Envelope)
						history = append(history, LiveRecord{Assistant: update.Envelope})
						_, err := writer.RecordLive(LiveRecordProfile{QuerySource: "sdk"}, history, nil)
						return err
					}
					for _, event := range pendingEvents(record.Input.Prefix) {
						if err := scope.Frame(cooperative.FrameFeed, func() error { return controller.Feed(event, streamhistory.Timing{}, emit) }); err != nil {
							return err
						}
					}
					close(files.arm)
					tail, err := scope.AwaitBytes(cooperative.StreamNext, func(ctx context.Context) ([]byte, error) {
						select {
						case <-tailReady:
							return []byte(record.Input.Tail), nil
						case <-ctx.Done():
							return nil, context.Cause(ctx)
						}
					})
					if err != nil {
						return err
					}
					for _, event := range pendingEvents(string(tail)) {
						if err = scope.Frame(cooperative.FrameFeed, func() error { return controller.Feed(event, streamhistory.Timing{}, emit) }); err != nil {
							return err
						}
					}
					close(modelComplete)
					checkpoint, err = pending.Begin(scope)
					if err != nil {
						return err
					}
					return checkpoint.Await(scope)
				})
			}()
			awaitSignal(t, files.held, "real private "+record.Mode)
			close(tailReady)
			awaitSignal(t, modelComplete, "real Controller delta/stop while FS held")
			pendingCall(t, owner, cooperative.Read, func(*cooperative.Scope) error {
				if len(assistants) != 2 {
					return errors.New("actual prefix did not produce two envelopes")
				}
				last, err := assistants[1].MessageJSON()
				if err != nil {
					return err
				}
				if !bytes.Contains(last, []byte(`"output_tokens":9`)) || !bytes.Contains(last, []byte(`"stop_reason":"end_turn"`)) {
					return fmt.Errorf("late live usage missing: %s", last)
				}
				if checkpoint == nil {
					return errors.New("checkpoint not admitted")
				}
				select {
				case <-checkpoint.Done():
					return errors.New("checkpoint settled before FS release")
				default:
				}
				disk, err := os.ReadFile(writer.target())
				if err != nil {
					return err
				}
				if bytes.Contains(disk, []byte(`"type":"assistant"`)) {
					return errors.New("assistant appended while FS held")
				}
				return nil
			})
			close(files.release)
			select {
			case err = <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(5 * time.Second):
				t.Fatal("checkpoint did not join independent job")
			}
			awaitSignal(t, files.closed, "descriptor closed before checkpoint settlement")
			if err = pending.Close(context.Background()); err != nil {
				t.Fatal(err)
			}
			actual := map[string]string{}
			err = filepath.WalkDir(filepath.Join(home, ".claude", "projects"), func(path string, entry fs.DirEntry, err error) error {
				if err != nil {
					return err
				}
				if entry.IsDir() || !strings.HasSuffix(path, ".jsonl") {
					return nil
				}
				disk, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				relative, _ := filepath.Rel(home, path)
				relative = strings.ReplaceAll(relative, sanitize(home), "<private-project-key>")
				actual[relative] = strings.ReplaceAll(string(disk), home, "<private-home>")
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
			expected, _ := json.Marshal(record.Expected.Files)
			got, _ := json.Marshal(actual)
			if !bytes.Equal(expected, got) {
				t.Fatalf("complete actual Project bytes differ\nexpected %s\nactual %s", expected, got)
			}
		})
	}
}
