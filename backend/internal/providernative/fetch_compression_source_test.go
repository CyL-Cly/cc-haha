//go:build darwin

package providernative

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"net"
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
	"github.com/andybalholm/brotli"
	"github.com/klauspost/compress/zstd"
)

func TestNativeFetchCompressionBytesAndPhysicalHeadersMatchActualBun(t *testing.T) {
	for _, secure := range []bool{false, true} {
		t.Run(fmt.Sprint("TLS=", secure), func(t *testing.T) { compareNativeFetchCompression(t, secure) })
	}
}
func compareNativeFetchCompression(t *testing.T, secure bool) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun not configured")
	}
	payload := []byte("ACTUAL_FETCH_\x00_PAYLOAD_日本語\n")
	encode := func(codec string, in []byte) []byte {
		var buffer bytes.Buffer
		var writer io.WriteCloser
		switch codec {
		case "gzip":
			writer = gzip.NewWriter(&buffer)
		case "deflate":
			writer = zlib.NewWriter(&buffer)
		case "raw-deflate":
			writer, _ = flate.NewWriter(&buffer, flate.DefaultCompression)
		case "br":
			writer = brotli.NewWriter(&buffer)
		case "zstd":
			writer, _ = zstd.NewWriter(&buffer, zstd.WithEncoderConcurrency(1))
		default:
			return in
		}
		if _, err := writer.Write(in); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		return buffer.Bytes()
	}
	type profile struct {
		encoding string
		data     []byte
		headers  map[string]string
	}
	profiles := []profile{}
	for _, codec := range []string{"gzip", "deflate", "raw-deflate", "br", "zstd", "unknown", "identity"} {
		encoding := codec
		if codec == "raw-deflate" {
			encoding = "deflate"
		}
		profiles = append(profiles, profile{encoding, encode(codec, payload), nil})
	}
	profiles = append(profiles,
		profile{"x-gzip", encode("gzip", payload), nil},
		profile{"GZIP", encode("gzip", payload), nil},
		profile{"gzip", nil, nil},
		profile{"deflate, gzip", encode("gzip", encode("deflate", payload)), nil},
		profile{"gzip, unknown", encode("gzip", payload), nil},
		profile{"gzip", encode("gzip", payload), map[string]string{"Accept-Encoding": "identity", "Connection": "close"}},
		profile{"gzip", encode("gzip", payload), map[string]string{"Accept-Encoding": ""}},
		profile{"gzip", encode("gzip", payload), nil},
	)
	var mu sync.Mutex
	requests := map[string]map[string]string{}
	peer := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		i, _ := strconv.Atoi(strings.TrimPrefix(r.URL.Path, "/"))
		mu.Lock()
		requests[r.Header.Get("X-Fixture-Run")+"/"+strconv.Itoa(i)] = map[string]string{"acceptEncoding": r.Header.Get("Accept-Encoding"), "connection": r.Header.Get("Connection")}
		mu.Unlock()
		w.Header().Set("Content-Encoding", profiles[i].encoding)
		w.Header().Set("Content-Length", strconv.Itoa(len(profiles[i].data)))
		_, _ = w.Write(profiles[i].data)
	}))
	defer peer.Close()
	var trust *tls.Config
	var ca string
	if secure {
		peer.StartTLS()
		ca = string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}))
		roots := x509.NewCertPool()
		if !roots.AppendCertsFromPEM([]byte(ca)) {
			t.Fatal("private TLS CA")
		}
		trust = &tls.Config{RootCAs: roots}
	} else {
		peer.Start()
	}
	rows := []map[string]any{}
	for i, p := range profiles {
		rows = append(rows, map[string]any{"url": peer.URL + "/" + strconv.Itoa(i), "headers": p.headers, "ca": ca})
	}
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-fetch-compression.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_FETCH_COMPRESSION_FIXTURE=1", "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	data, _ := json.Marshal(rows)
	command.Stdin = bytes.NewReader(data)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	out, err := command.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source []map[string]any
	if err = json.Unmarshal(out, &source); err != nil || len(source) != len(rows) {
		t.Fatal(err, string(out))
	}
	owner := cooperative.New()
	defer owner.Close()
	err = owner.Run(ctx, func(scope *cooperative.Scope) error {
		for i, p := range profiles {
			request, _ := http.NewRequestWithContext(ctx, http.MethodGet, rows[i]["url"].(string), nil)
			request.Header.Set("X-Fixture-Run", "go")
			for name, value := range p.headers {
				request.Header.Set(name, value)
			}
			if i == len(profiles)-1 {
				request.Header["Accept-Encoding"] = nil
				request.Header["Connection"] = nil
			}
			driver, e := New(owner, scope, nil)
			if e != nil {
				return e
			}
			response, e := driver.OpenFetch(request, &provider.RawHTTP1Exchange{VerifiedTLS: trust})
			if e != nil {
				return fmt.Errorf("profile %d %s: %w", i, p.encoding, e)
			}
			bodyDriver, e := provider.NewOwnerBodyIO(driver, func(kind provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
				return scope.AwaitBytes(cooperative.StreamNext, work)
			})
			if e != nil {
				return e
			}
			body, e := bodyDriver.ReadFetchBody(response.Response.Body)
			_ = response.Response.Body.Close()
			if e != nil {
				return fmt.Errorf("profile %d body: %w", i, e)
			}
			actual := map[string]any{"status": float64(response.Response.StatusCode), "encoding": response.Response.Header.Get("Content-Encoding"), "length": response.Response.Header.Get("Content-Length"), "body": base64.StdEncoding.EncodeToString(body)}
			if !reflect.DeepEqual(actual, source[i]) {
				return fmt.Errorf("profile %d Go %#v Source %#v", i, actual, source[i])
			}
			mu.Lock()
			same := reflect.DeepEqual(requests["go/"+strconv.Itoa(i)], requests["source/"+strconv.Itoa(i)])
			mu.Unlock()
			if !same {
				return fmt.Errorf("profile %d physical headers differ: %#v", i, requests)
			}
			for _, mode := range []string{"raw", "owned-worker"} {
				var result *provider.HTTPExchangeResponse
				var cancel context.CancelFunc
				if mode == "raw" {
					result, e = (&provider.RawHTTP1Exchange{VerifiedTLS: trust}).Do(request)
				} else {
					owned, e2 := provider.NewOwnerOpenIO(func(_ provider.OwnerIOWait, work func(context.Context) ([]byte, error)) ([]byte, error) {
						return scope.AwaitBytes(cooperative.StreamNext, work)
					})
					if e2 != nil {
						return e2
					}
					headers := request.Header.Clone()
					client := provider.NewAnthropic(provider.AnthropicConfig{APIKey: "fake-owned-loopback", BaseURL: peer.URL, Timeout: time.Second, DefaultHeaders: headers, HTTPExchange: &provider.RawHTTP1Exchange{VerifiedTLS: trust}, OwnerOpenIO: owned})
					result, cancel, e = client.OpenPreparedResponse(ctx, &provider.MessagesRequest{Path: "/" + strconv.Itoa(i), Body: []byte(`{}`)})
				}
				if e != nil {
					return fmt.Errorf("%s profile %d: %w", mode, i, e)
				}
				var data []byte
				if mode == "raw" {
					data, e = io.ReadAll(result.Response.Body)
				} else {
					data, e = scope.AwaitBytes(cooperative.StreamNext, func(context.Context) ([]byte, error) { return io.ReadAll(result.Response.Body) })
				}
				_ = result.Response.Body.Close()
				if cancel != nil {
					cancel()
				}
				if e != nil {
					return fmt.Errorf("%s profile %d read: %w", mode, i, e)
				}
				actual = map[string]any{"status": float64(result.Response.StatusCode), "encoding": result.Response.Header.Get("Content-Encoding"), "length": result.Response.Header.Get("Content-Length"), "body": base64.StdEncoding.EncodeToString(data)}
				if !reflect.DeepEqual(actual, source[i]) {
					return fmt.Errorf("%s profile %d Go %#v Source %#v", mode, i, actual, source[i])
				}
				mu.Lock()
				same = reflect.DeepEqual(requests["go/"+strconv.Itoa(i)], requests["source/"+strconv.Itoa(i)])
				mu.Unlock()
				if !same {
					return fmt.Errorf("%s profile %d physical headers differ", mode, i)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestCompressedNativeBodyAbortStopAndAdministrativeClose(t *testing.T) {
	for _, mode := range []string{"model-cancel", "consumer-stop", "owner-close"} {
		t.Run(mode, func(t *testing.T) {
			prefix := make(chan struct{})
			returned := make(chan error, 1)
			origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
				_, _ = io.WriteString(conn, "HTTP/1.1 200 OK\r\nContent-Type: text/event-stream\r\nContent-Encoding: gzip\r\nTransfer-Encoding: chunked\r\n\r\n")
				var buffer bytes.Buffer
				writer := gzip.NewWriter(&buffer)
				_, _ = io.WriteString(writer, "event: message_start\ndata: {\"type\":\"message_start\"}\n\n")
				_ = writer.Flush()
				prefix := append([]byte(nil), buffer.Bytes()...)
				_, _ = fmt.Fprintf(conn, "%x\r\n%s\r\n", len(prefix), prefix)
				_ = writer.Close()
			})
			owner := cooperative.New()
			defer owner.Close()
			caller, cancel := context.WithCancel(context.Background())
			defer cancel()
			var stream *provider.RawMessageStream
			go func() {
				returned <- owner.Run(context.Background(), func(scope *cooperative.Scope) error {
					config, err := bodyConfig(owner, scope, origin, nil)
					if err != nil {
						return err
					}
					stream, err = provider.NewAnthropic(config).OpenMessagesStream(caller, originalPrepared())
					if err != nil {
						return err
					}
					defer stream.Close()
					raw, err := stream.Next()
					if err != nil || string(raw) != `{"type":"message_start"}` {
						return fmt.Errorf("first demand changed: %s %v", raw, err)
					}
					close(prefix)
					_, err = stream.Next()
					if mode == "owner-close" {
						if !provider.IsOwnerWaitFailure(err) || !errors.Is(err, cooperative.ErrClosed) || stream.AbortCause() != nil {
							return fmt.Errorf("administrative body close changed: %v cause=%v", err, stream.AbortCause())
						}
						return nil
					}
					if mode == "model-cancel" && err != io.EOF {
						return fmt.Errorf("quiet model abort changed: %v", err)
					}
					if mode == "consumer-stop" && err != io.EOF && !errors.Is(err, net.ErrClosed) {
						return fmt.Errorf("concurrent resource close ownership: %v", err)
					}
					if mode == "model-cancel" && stream.AbortCause() != context.Canceled {
						return fmt.Errorf("caller abort reason: %v", stream.AbortCause())
					}
					if mode == "consumer-stop" && stream.AbortCause() != nil {
						return fmt.Errorf("consumer stop became model abort: %v", stream.AbortCause())
					}
					return nil
				})
			}()
			<-prefix
			if mode == "owner-close" {
				owner.Close()
			} else {
				receipt, err := owner.Submit(context.Background(), cooperative.Cancel, func(*cooperative.Scope) error {
					if mode == "model-cancel" {
						cancel()
						return nil
					}
					return stream.Close()
				})
				if err != nil {
					t.Fatal(err)
				}
				select {
				case <-receipt.Done():
				case <-time.After(time.Second):
					t.Fatal("cancel/close callback could not run during native body wait")
				}
			}
			select {
			case err := <-returned:
				if err != nil && !(mode == "owner-close" && errors.Is(err, cooperative.ErrClosed)) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("native body worker did not join")
			}
			joinedPeer(t, peer)
			if mode != "owner-close" {
				if err := owner.Run(context.Background(), func(*cooperative.Scope) error { return nil }); err != nil {
					t.Fatal("model resource cancellation closed owner lifetime", err)
				}
			}
		})
	}
}

func TestCompressedNativeBridgeHeadersFulfillBeforeDecoderReadsBody(t *testing.T) {
	cleared := make(chan struct{})
	peerBodyWritten := make(chan struct{})
	origin, peer := nativePeer(t, func(conn net.Conn, _ *http.Request) {
		var compressed bytes.Buffer
		writer := gzip.NewWriter(&compressed)
		_, _ = io.WriteString(writer, "same")
		_ = writer.Close()
		_, _ = fmt.Fprintf(conn, "HTTP/1.1 200 OK\r\nContent-Type: application/json\r\nContent-Encoding: gzip\r\nContent-Length: %d\r\nrequest-id: actual\r\n\r\n", compressed.Len())
		<-cleared
		_, _ = conn.Write(compressed.Bytes())
		close(peerBodyWritten)
	})
	owner := cooperative.New()
	defer owner.Close()
	var body io.ReadCloser
	var kinds []string
	err := owner.Run(context.Background(), func(scope *cooperative.Scope) error {
		driver, err := New(owner, scope, func(event provider.OwnerNativeEvent) {
			kinds = append(kinds, event.Kind)
			if event.Kind == "timer-clear" {
				close(cleared)
			}
		})
		if err != nil {
			return err
		}
		config := nativeConfig(origin)
		config.OwnerNativeIO = driver
		exchange, cancel, err := provider.NewAnthropic(config).OpenPreparedResponse(context.Background(), originalPrepared())
		if err != nil {
			return err
		}
		defer cancel()
		body = exchange.Response.Body
		before, err := provider.InspectNativeOwnerBody(body)
		if err != nil || before.Closed {
			return errors.New("short Await closed successful body lease")
		}
		if exchange.Response.StatusCode != 200 || exchange.OriginalHeaders == nil || exchange.Response.Header.Get("request-id") != "actual" {
			return errors.New("owner response projection changed")
		}
		raw, err := scope.AwaitBytes(cooperative.StreamNext, func(ctx context.Context) ([]byte, error) { return io.ReadAll(body) })
		if err != nil || string(raw) != "same" {
			return errors.New("body did not outlive fetch finally")
		}
		if err = body.Close(); err != nil {
			return err
		}
		return body.Close()
	})
	if err != nil {
		t.Fatal(err)
	}
	<-peerBodyWritten
	assertClosed(t, body)
	joinedPeer(t, peer)
	if strings.Join(kinds, ",") != "timer-arm,fetch-enter,fetch-fulfilled,timer-clear" {
		t.Fatal(kinds)
	}
}
