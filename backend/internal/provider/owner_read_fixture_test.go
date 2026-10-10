package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/requestmapping"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

type originalReadFixture struct {
	Name, End, Mode, Wire, Home, Origin string
	Chunks                              [][]byte
}
type originalChunksReader struct {
	chunks [][]byte
	offset int
	end    string
}

func (r *originalChunksReader) Close() error { return nil }
func (r *originalChunksReader) Read(out []byte) (int, error) {
	if len(r.chunks) == 0 {
		if r.end == "eof" {
			return 0, io.EOF
		}
		if r.end == "abort-error" {
			return 0, context.Canceled
		}
		return 0, errors.New("original read fault")
	}
	n := copy(out, r.chunks[0][r.offset:])
	r.offset += n
	if r.offset == len(r.chunks[0]) {
		r.chunks = r.chunks[1:]
		r.offset = 0
	}
	if len(r.chunks) == 0 {
		if r.end == "eof" {
			return n, io.EOF
		}
		if r.end == "abort-error" {
			return n, context.Canceled
		}
		return n, errors.New("original read fault")
	}
	return n, nil
}
func TestOwnerReadFixture(t *testing.T) {
	if os.Getenv("CC_HAHA_OWNER_READ_FIXTURE") == "" {
		t.Skip("private Read differential child")
	}
	if os.Getenv("CC_HAHA_OWNER_READ_FIXTURE") != "1" {
		t.Fatal("explicit private Read profile required")
	}
	var input originalReadFixture
	decoder := json.NewDecoder(os.Stdin)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		t.Fatal(err)
	}
	stat, statErr := os.Lstat(input.Home)
	if statErr != nil || stat.Mode()&os.ModeSymlink != 0 || !stat.IsDir() || !filepath.IsAbs(input.Home) {
		t.Fatal("disposable HOME/config required")
	}
	home, err := filepath.EvalSymlinks(input.Home)
	if err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	cwd, _ = filepath.EvalSymlinks(cwd)
	root, rootErr := filepath.EvalSymlinks(os.Getenv("CC_HAHA_FIXTURE_TEMP_ROOT"))
	configStat, configErr := os.Lstat(filepath.Join(input.Home, ".claude"))
	if rootErr != nil || !filepath.IsAbs(root) || configErr != nil || configStat.Mode()&os.ModeSymlink != 0 || !configStat.IsDir() {
		t.Fatal("disposable HOME/config requires allocation root")
	}
	if input.Home != os.Getenv("HOME") || home != cwd || !strings.HasPrefix(home, root+string(os.PathSeparator)) || os.Getenv("USERPROFILE") != input.Home || os.Getenv("CLAUDE_CONFIG_DIR") != filepath.Join(input.Home, ".claude") {
		t.Fatal("disposable HOME/config required")
	}
	if input.Mode != "" && !regexp.MustCompile(`^http://127\.0\.0\.1:[0-9]+$`).MatchString(input.Origin) {
		t.Fatal("exact loopback required")
	}
	events := []string{}
	diagnostics := []any{}
	nextCalls := 0
	var fault any
	var callerAborted any
	consume := func(stream *RawMessageStream) error {
		defer stream.Close()
		for {
			nextCalls++
			raw, failure := stream.Next()
			if failure == nil {
				events = append(events, string(protocol.StringifyValidatedJavaScriptJSON(raw)))
				continue
			}
			if !errors.Is(failure, io.EOF) {
				class, name, message := "Error", "Error", failure.Error()
				var sdk *SDKStreamError
				if errors.As(failure, &sdk) {
					class, name = sdk.Class, sdk.Name
				}
				fault = map[string]any{"class": class, "name": name, "message": message}
			}
			break
		}
		return nil
	}
	if input.Mode == "" {
		body := &originalChunksReader{chunks: input.Chunks, end: input.End}
		stream := &RawMessageStream{ctx: context.Background(), body: body, cancel: func() {}, decoder: protocol.NewSDKSSEDecoder(body), Headers: http.Header{"Request-Id": []string{"original-request"}}, diagnose: func(label string, value any) { diagnostics = append(diagnostics, []any{label, value}) }}
		if err := consume(stream); err != nil {
			t.Fatal(err)
		}
	} else {
		owner := cooperative.New()
		defer owner.Close()
		caller, cancel := context.WithCancel(context.Background())
		defer cancel()
		err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
			driver, _ := NewOwnerOpenIO(func(kind OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				raw, failure := readAwait(scope, kind, work)
				if kind == OwnerBodyRead && input.Mode == "ready-cancel" && len(raw) > 0 {
					cancel()
				}
				return raw, failure
			})
			params, _ := requestmapping.ParseValue([]byte(`{"model":"fixture-model","messages":[{"role":"user","content":"Original Read input"}],"max_tokens":10,"stream":true}`))
			prepared, failure := PrepareBetaMessages(params, BetaMessageOptions{})
			if failure != nil {
				return failure
			}
			client := NewAnthropic(AnthropicConfig{BaseURL: input.Origin, APIKey: "original-private-key", Timeout: 3 * time.Second, HTTPExchange: &RawHTTP1Exchange{}, OwnerOpenIO: driver, StreamDiagnostic: func(label string, value any) { diagnostics = append(diagnostics, []any{label, value}) }})
			stream, failure := client.OpenMessagesStream(caller, prepared)
			if failure != nil {
				return failure
			}
			if input.Mode == "pending-cancel" {
				entered := make(chan struct{})
				lease := soleReadLease(driver)
				lease.body = &readStartObserver{ReadCloser: lease.body, entered: entered}
				go func() {
					<-entered
					response, failure := http.Get(input.Origin + "/read-pending")
					if failure == nil {
						io.Copy(io.Discard, response.Body)
						response.Body.Close()
					}
					receipt, failure := owner.Submit(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error { cancel(); return nil })
					if failure == nil {
						receipt.Wait()
					}
				}()
			}
			failure = consume(stream)
			callerAborted = caller.Err() != nil
			if driver.LeaseCount() != 0 {
				return errors.New("read lease not closed")
			}
			return failure
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	raw, err := json.Marshal(map[string]any{"observation": map[string]any{"events": events, "nextCalls": nextCalls, "diagnostics": diagnostics, "fault": fault, "callerAborted": callerAborted}})
	if err != nil {
		t.Fatal(err)
	}
	fmt.Printf("OWNER_READ_RESULT:%s\n", raw)
}
