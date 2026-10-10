package cliauth_test

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"encoding/pem"
	"github.com/andybalholm/brotli"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/cliauth"
	"github.com/NanmiCoder/cc-haha/backend/internal/desktopoauth"
)

func TestActualSourceCLIRefreshLifetimeCoercion(t *testing.T) {
	for _, row := range []struct{ name, expiry string }{{"missing", ""}, {"null", "null"}, {"string", `"3600"`}, {"empty", `""`}, {"invalid", `"invalid"`}, {"boolean", "true"}, {"array", "[3600]"}, {"object", "{}"}, {"status201", "3600"}, {"tls-untrusted", "3600"}, {"gzip", "3600"}, {"compress", "3600"}, {"deflate", "3600"}, {"raw-deflate", "3600"}, {"br", "3600"}, {"x-gzip", "3600"}, {"x-compress", "3600"}, {"GZIP", "3600"}, {"unknown", "3600"}, {"comma", "3600"}, {"gzip-zlib", "3600"}, {"bad-gzip", "3600"}, {"truncated-gzip", "3600"}, {"bad-deflate", "3600"}, {"bad-br", "3600"}, {"BOM", "3600"}, {"truncated-gzip-data", "3600"}, {"timeout", "3600"}} {
		t.Run(row.name, func(t *testing.T) {
			home, e := filepath.EvalSymlinks(t.TempDir())
			if e != nil {
				t.Fatal(e)
			}
			config := filepath.Join(home, ".claude")
			_ = os.Mkdir(config, 0700)
			_ = os.WriteFile(filepath.Join(config, "settings.json"), []byte(`{}`), 0600)
			body := `{"access_token":"fixture-access","refresh_token":"fixture-next","scope":"user:profile user:inference"`
			if row.expiry != "" {
				body += `,"expires_in":` + row.expiry
			}
			body += `}`
			stopPeer := make(chan struct{})
			peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if row.name == "timeout" {
					_, _ = io.Copy(io.Discard, r.Body)
					_ = r.Body.Close()
					select {
					case <-r.Context().Done():
					case <-stopPeer:
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				if r.URL.Path == "/v1/oauth/token" {
					if row.name == "status201" {
						w.WriteHeader(http.StatusCreated)
					}
					switch row.name {
					case "gzip", "compress", "x-gzip", "x-compress", "GZIP":
						w.Header().Set("Content-Encoding", row.name)
						codec := gzip.NewWriter(w)
						_, _ = codec.Write([]byte(body))
						_ = codec.Close()
					case "deflate", "gzip-zlib":
						encoding := "deflate"
						if row.name == "gzip-zlib" {
							encoding = "gzip"
						}
						w.Header().Set("Content-Encoding", encoding)
						codec := zlib.NewWriter(w)
						_, _ = codec.Write([]byte(body))
						_ = codec.Close()
					case "raw-deflate":
						w.Header().Set("Content-Encoding", "deflate")
						codec, _ := flate.NewWriter(w, flate.DefaultCompression)
						_, _ = codec.Write([]byte(body))
						_ = codec.Close()
					case "br":
						w.Header().Set("Content-Encoding", "br")
						codec := brotli.NewWriter(w)
						_, _ = codec.Write([]byte(body))
						_ = codec.Close()
					case "bad-gzip", "bad-deflate", "bad-br":
						encoding := strings.TrimPrefix(row.name, "bad-")
						w.Header().Set("Content-Encoding", encoding)
						_, _ = w.Write([]byte("owned-invalid-compressed-entity"))
					case "BOM":
						_, _ = w.Write(append([]byte{0xef, 0xbb, 0xbf}, []byte(body)...))
					case "truncated-gzip-data":
						var buffer bytes.Buffer
						codec := gzip.NewWriter(&buffer)
						_, _ = codec.Write([]byte(body))
						_ = codec.Close()
						data := buffer.Bytes()
						w.Header().Set("Content-Encoding", "gzip")
						_, _ = w.Write(data[:len(data)/2])
					case "truncated-gzip":
						var buffer bytes.Buffer
						codec := gzip.NewWriter(&buffer)
						_, _ = codec.Write([]byte(body))
						_ = codec.Close()
						data := buffer.Bytes()
						w.Header().Set("Content-Encoding", "gzip")
						_, _ = w.Write(data[:len(data)-5])
					case "unknown", "comma":
						encoding := "owned-unknown"
						if row.name == "comma" {
							encoding = "gzip, br"
						}
						w.Header().Set("Content-Encoding", encoding)
						_, _ = w.Write([]byte(body))
					default:
						_, _ = w.Write([]byte(body))
					}
				} else {
					http.Error(w, "owned unavailable profile", 503)
				}
			}))
			defer peer.Close()
			defer close(stopPeer)
			cert := filepath.Join(home, "cert.pem")
			_ = os.WriteFile(cert, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: peer.Certificate().Raw}), 0600)
			fixture, _ := filepath.Abs("../../testdata/native-cli-auth-login.fixture.ts")
			profile, _ := filepath.Abs("../../testdata/cli-auth-loopback-only.sb")
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "/usr/bin/sandbox-exec", "-f", profile, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			command.Dir = home
			command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + config, "TMPDIR=" + home, "PATH=/usr/bin:/bin", "USER=fixture", "BUN_OPTIONS=--no-env-file", "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "CC_HAHA_CLI_LOGIN_FIXTURE=1", "CC_HAHA_CLI_LOGIN_CLIENT_ONLY=1", "CC_HAHA_CLI_LOGIN_PEER=" + peer.URL, "NODE_EXTRA_CA_CERTS=" + cert, "DISABLE_TELEMETRY=1", "CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC=1", "CLAUDE_CODE_LOCAL_SKIP_REMOTE_PREFETCH=1", "USER_TYPE=external", "NODE_ENV=production"}
			if row.name == "tls-untrusted" {
				for i, env := range command.Env {
					if strings.HasPrefix(env, "NODE_EXTRA_CA_CERTS=") {
						command.Env = append(command.Env[:i], command.Env[i+1:]...)
						break
					}
				}
			}
			var stderr bytes.Buffer
			command.Stderr = &stderr
			raw, e := command.Output()
			if e != nil {
				t.Fatal(e, stderr.String())
			}
			var original map[string]any
			if e = json.Unmarshal(raw, &original); e != nil {
				t.Fatal(e, string(raw))
			}
			configGo, e := desktopoauth.ResolveConfig(map[string]string{})
			if e != nil {
				t.Fatal(e)
			}
			configGo.BaseAPIURL = peer.URL
			configGo.TokenURL = peer.URL + "/v1/oauth/token"
			transport := peer.Client()
			if row.name == "tls-untrusted" {
				transport = &http.Client{}
			}
			client := cliauth.OAuthClient{Config: configGo, HTTP: transport, NowMillis: func() float64 { return 1700000000000 }}
			actual, e := client.RefreshClaude(ctx, "fixture-refresh", []string{"user:profile", "user:inference"})
			if original["error"] != nil {
				if e == nil || e.Error() != original["error"] {
					t.Fatalf("actual Source client error %v Go %v", original["error"], e)
				}
				if original["sslHint"] != nil && cliauth.OAuthSSLErrorHint(e) != original["sslHint"] {
					t.Fatalf("actual Source TLS hint %v Go %v", original["sslHint"], cliauth.OAuthSSLErrorHint(e))
				}
				return
			}
			if e != nil {
				t.Fatal(e)
			}
			actualRaw, _ := actual.MarshalJSON()
			var actualMap map[string]any
			_ = json.Unmarshal(actualRaw, &actualMap)
			if !reflect.DeepEqual(original["tokens"], actualMap) {
				t.Fatalf("actual Source response %s Go %s", raw, actualRaw)
			}
		})
	}
}
