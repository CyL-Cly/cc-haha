//go:build darwin

package providernative

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/provider"
)

func TestUnderlyingFetchRedirectModesMethodsCredentialsAndHeadersMatchActualBun(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	var mu sync.Mutex
	requests := map[string][]map[string]string{}
	var rows []map[string]any
	var first, second string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		data, _ := io.ReadAll(r.Body)
		i, _ := strconv.Atoi(r.URL.Query().Get("i"))
		mu.Lock()
		key := r.Header.Get("X-Fixture-Run") + "/" + strconv.Itoa(i)
		requests[key] = append(requests[key], map[string]string{"method": r.Method, "body": string(data), "authorization": r.Header.Get("Authorization"), "cookie": r.Header.Get("Cookie"), "contentType": r.Header.Get("Content-Type"), "custom": r.Header.Get("X-Custom"), "path": r.URL.RequestURI()})
		mu.Unlock()
		if r.URL.Path == "/target" {
			w.Header().Add("X-Duplicate", "first")
			w.Header().Add("X-Duplicate", "second")
			_, _ = io.WriteString(w, "ACTUAL_REDIRECT_TARGET")
			return
		}
		row := rows[i]
		destination := "/target?i=" + strconv.Itoa(i)
		if row["destination"] == "port" {
			destination = second + destination
		} else if row["destination"] == "host" {
			destination = strings.Replace(second, "127.0.0.1", "localhost", 1) + destination
		} else if row["destination"] == "loop" {
			destination = first + "/redirect?i=" + strconv.Itoa(i)
		} else if row["destination"] == "missing" {
			destination = ""
		}
		if destination != "" {
			w.Header().Set("Location", destination)
		}
		w.Header().Add("X-Duplicate", "first")
		w.Header().Add("X-Duplicate", "second")
		w.WriteHeader(row["status"].(int))
		_, _ = io.WriteString(w, "ACTUAL_REDIRECT_BODY")
	})
	a, b := httptest.NewServer(handler), httptest.NewServer(handler)
	defer a.Close()
	defer b.Close()
	first, second = a.URL, b.URL
	for _, status := range []int{301, 302, 303, 307, 308} {
		for _, method := range []string{"POST", "PUT", "GET"} {
			for _, destination := range []string{"same", "port", "host"} {
				rows = append(rows, map[string]any{"status": status, "method": method, "destination": destination, "mode": "follow"})
			}
		}
	}
	for _, row := range []map[string]any{{"status": 302, "method": "POST", "destination": "same", "mode": "manual"}, {"status": 302, "method": "POST", "destination": "same", "mode": "error"}, {"status": 302, "method": "POST", "destination": "missing", "mode": "follow"}, {"status": 300, "method": "POST", "destination": "same", "mode": "follow"}, {"status": 302, "method": "POST", "destination": "loop", "mode": "follow"}} {
		rows = append(rows, row)
	}
	for i, row := range rows {
		row["url"] = first + "/redirect?i=" + strconv.Itoa(i)
		row["headers"] = map[string]string{"Authorization": "Bearer private-fake-token", "Cookie": "private-cookie=fixture", "X-Custom": "kept", "Content-Type": "application/json"}
		if row["method"] != "GET" {
			row["body"] = "PRIVATE_ACTUAL_BODY"
		}
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-fetch-redirect.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FETCH_REDIRECT_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	payload, _ := json.Marshal(rows)
	command.Stdin = bytes.NewReader(payload)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	data, err := command.Output()
	if err != nil {
		t.Fatal("actual fetch redirect Source", err, stderr.String())
	}
	var expected []map[string]any
	if err = json.Unmarshal(data, &expected); err != nil || len(expected) != len(rows) {
		t.Fatal("Source redirect results", err, string(data))
	}
	owner := cooperative.New()
	defer owner.Close()
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, row := range rows {
			request, err := http.NewRequestWithContext(ctx, row["method"].(string), row["url"].(string), strings.NewReader(stringValueRedirect(row["body"])))
			if err != nil {
				return err
			}
			for key, value := range row["headers"].(map[string]string) {
				request.Header.Set(key, value)
			}
			request.Header.Set("X-Fixture-Run", "go")
			driver, err := New(owner, scope, nil)
			if err != nil {
				return err
			}
			response, failure := driver.OpenFetchWithOptions(request, &provider.RawHTTP1Exchange{}, provider.NativeFetchOptions{Redirect: row["mode"].(string)})
			actual := map[string]any{}
			if failure != nil {
				actual["error"], actual["code"] = failure.Error(), nil
				if typed, ok := failure.(interface{ Code() string }); ok {
					actual["code"] = typed.Code()
				}
			} else {
				bodyIO, err := provider.NewOwnerBodyIO(driver, func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
					return scope.AwaitBytes(cooperative.StreamNext, work)
				})
				if err != nil {
					return err
				}
				body, err := bodyIO.ReadFetchBody(response.Response.Body)
				_ = response.Response.Body.Close()
				if err != nil {
					return err
				}
				actual = map[string]any{"status": float64(response.Response.StatusCode), "url": response.Response.Request.URL.String(), "location": nil, "duplicate": response.Response.Header.Get("X-Duplicate"), "text": string(body)}
				if location := response.Response.Header.Get("Location"); location != "" {
					actual["location"] = location
				}
			}
			if !reflect.DeepEqual(actual, expected[i]) {
				t.Errorf("row%d %+v Go%v Source%v", i, row, actual, expected[i])
			}
			mu.Lock()
			goRequests, sourceRequests := requests["go/"+strconv.Itoa(i)], requests["source/"+strconv.Itoa(i)]
			mu.Unlock()
			if !reflect.DeepEqual(goRequests, sourceRequests) {
				t.Errorf("row%d redirect physical requests Go%v Source%v", i, goRequests, sourceRequests)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func stringValueRedirect(value any) string { text, _ := value.(string); return text }
