package cloudauth

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"
)

func TestActualAWSFromHTTPPhysicalTokenFileAndAmbientProxyContract(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("actual Source Bun required")
	}
	var targets, proxies atomic.Int32
	failures := make(chan string, 2)
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		targets.Add(1)
		if r.Method != "GET" || r.URL.Path != "/credentials" || r.Header.Get("Authorization") != "TOKEN_FILE_PRIVATE" {
			failures <- "wrong actual target/token file request"
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"AccessKeyId":"HTTP_PRIVATE","SecretAccessKey":"HTTP_SECRET","Token":"HTTP_SESSION","Expiration":"2030-01-02T03:04:05Z"}`)
	}))
	defer target.Close()
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proxies.Add(1)
		request, err := http.NewRequestWithContext(r.Context(), r.Method, target.URL+r.URL.RequestURI(), r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		request.Header = r.Header.Clone()
		direct := &http.Client{Transport: &http.Transport{Proxy: nil}}
		defer direct.CloseIdleConnections()
		response, err := direct.Do(request)
		if err != nil {
			t.Error(err)
			w.WriteHeader(500)
			return
		}
		defer response.Body.Close()
		w.Header().Set("Content-Type", response.Header.Get("Content-Type"))
		w.WriteHeader(response.StatusCode)
		_, _ = io.Copy(w, response.Body)
	}))
	defer proxy.Close()
	home := t.TempDir()
	fixture, _ := filepath.Abs("../../../scripts/quality-gate/go-parity/native-aws-http-physical.fixture.ts")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bun, "--no-env-file", fixture)
	cmd.Dir = home
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "TMPDIR=" + home, "CC_HAHA_AWS_FIXTURE=1", "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	raw, _ := json.Marshal(map[string]string{"url": target.URL + "/credentials", "proxy": proxy.URL})
	cmd.Stdin = bytes.NewReader(raw)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err, stderr.String())
	}
	var source map[string]json.RawMessage
	if json.Unmarshal(out, &source) != nil {
		t.Fatal(string(out))
	}
	if source["error"] != nil {
		t.Fatalf("actual Source failed: %s", source["error"])
	}
	if targets.Load() != 1 || proxies.Load() != 1 {
		t.Fatal("Source Bun metadata SDK did not select actual frozen proxy", targets.Load(), proxies.Load())
	}
	token := filepath.Join(home, "native-token")
	if err = os.WriteFile(token, []byte("TOKEN_FILE_PRIVATE"), 0600); err != nil {
		t.Fatal(err)
	}
	proxyURL, _ := url.Parse(proxy.URL)
	nativeClient := &http.Client{Transport: &http.Transport{Proxy: http.ProxyURL(proxyURL)}}
	defer nativeClient.CloseIdleConnections()
	resolver, err := NewAWSResolver(AWSOptions{Home: home, Environment: map[string]string{"AWS_CONTAINER_CREDENTIALS_FULL_URI": target.URL + "/credentials", "AWS_CONTAINER_AUTHORIZATION_TOKEN_FILE": token, "HTTP_PROXY": proxy.URL, "HTTPS_PROXY": proxy.URL, "NO_PROXY": ""}, ReadFile: NativeAWSReadFile, Metadata: &Metadata{Do: nativeClient.Do}})
	if err != nil {
		t.Fatal(err)
	}
	value, err := resolver.Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	projected := map[string]any{}
	b, _ := json.Marshal(value)
	_ = json.Unmarshal(b, &projected)
	projected["expiration"] = value.Expiration.UTC().Format("2006-01-02T15:04:05.000Z")
	compareAWS(t, 0, projected, source["value"])
	if targets.Load() != 2 || proxies.Load() != 2 {
		t.Fatal("credential HTTP used unexpected transport", targets.Load(), proxies.Load())
	}
	select {
	case failure := <-failures:
		t.Fatal(failure)
	default:
	}
}
