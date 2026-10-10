//go:build darwin

package providernative

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestHeldMalformedCompressionReadErrorsMatchActualBun(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	codecs := []string{"gzip", "deflate", "br", "zstd", "gzip-socket"}
	releases := map[string]chan struct{}{}
	for i := range codecs {
		for _, side := range []string{"source", "go"} {
			releases[side+"/"+strconv.Itoa(i)] = make(chan struct{})
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		payload := []byte("THIS_IS_NOT_A_COMPRESSED_STREAM")
		encoding, length := codecs[i], len(payload)
		if encoding == "gzip-socket" {
			var buffer bytes.Buffer
			writer := gzip.NewWriter(&buffer)
			_, _ = writer.Write([]byte("ACTUAL_COMPRESSED_SOCKET"))
			_ = writer.Close()
			payload = buffer.Bytes()
			encoding = "gzip"
			length = len(payload) + 100
		}
		w.Header().Set("Content-Encoding", encoding)
		w.Header().Set("Content-Length", strconv.Itoa(length))
		w.WriteHeader(200)
		w.(http.Flusher).Flush()
		select {
		case <-releases[r.Header.Get("X-Fixture-Run")+"/"+strconv.Itoa(i)]:
		case <-ctx.Done():
			return
		case <-r.Context().Done():
			return
		}
		_, _ = w.Write(payload)
	}))
	defer peer.Close()
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-fetch-compression-errors.fixture.ts")
	rows := []map[string]string{}
	for i := range codecs {
		rows = append(rows, map[string]string{"url": peer.URL + "/" + strconv.Itoa(i)})
	}
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FETCH_COMPRESSION_ERROR_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	input, _ := json.Marshal(rows)
	command.Stdin = bytes.NewReader(input)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	stdout, err := command.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = command.Start(); err != nil {
		t.Fatal(err)
	}
	var source []map[string]any
	ready := 0
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) > 0 && line[0] == '[' {
			if err = json.Unmarshal(line, &source); err != nil {
				t.Fatal(err)
			}
			continue
		}
		var event struct{ Ready int }
		if err = json.Unmarshal(line, &event); err != nil {
			t.Fatal(err)
		}
		close(releases["source/"+strconv.Itoa(event.Ready)])
		ready++
	}
	if err = command.Wait(); err != nil {
		t.Fatal(err, stderr.String())
	}
	if ready != len(codecs) || len(source) != len(codecs) {
		t.Fatal("Source headers not independently fulfilled", ready, source)
	}
	owner := cooperative.New()
	defer owner.Close()
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i := range codecs {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, rows[i]["url"], nil)
			request.Header.Set("X-Fixture-Run", "go")
			driver, e := New(owner, scope, nil)
			if e != nil {
				return e
			}
			response, e := driver.OpenFetch(request, &provider.RawHTTP1Exchange{})
			if e != nil {
				return e
			}
			close(releases["go/"+strconv.Itoa(i)])
			body, e := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return scope.AwaitBytes(cooperative.StreamNext, work)
			})
			if e != nil {
				return e
			}
			_, failure := body.ReadFetchBody(response.Response.Body)
			_ = response.Response.Body.Close()
			if failure == nil {
				return fmt.Errorf("%s malformed body accepted", codecs[i])
			}
			code := ""
			if typed, ok := failure.(interface{ Code() string }); ok {
				code = typed.Code()
			}
			actual := map[string]any{"status": float64(response.Response.StatusCode), "error": map[string]any{"message": failure.Error(), "code": code}}
			if !reflect.DeepEqual(actual, source[i]) {
				return fmt.Errorf("%s Go %#v Source %#v", codecs[i], actual, source[i])
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
