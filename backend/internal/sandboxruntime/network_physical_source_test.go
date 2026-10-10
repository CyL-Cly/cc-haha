package sandboxruntime

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
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
	"testing"
	"time"
)

func waitFixturePath(t *testing.T, path string) []byte {
	t.Helper()
	deadline := time.Now().Add(8 * time.Second)
	for {
		bytes, e := os.ReadFile(path)
		if e == nil {
			return bytes
		}
		if time.Now().After(deadline) {
			t.Fatal("physical Source never ready", path, e)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func requestRawProxy(t *testing.T, port int, target string) string {
	t.Helper()
	connection, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer connection.Close()
	connection.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(connection, "POST %s HTTP/1.1\r\nHost: original.invalid\r\nContent-Length: 7\r\nX-Original: retained\r\nConnection: close, X-Drop\r\nX-Drop: hidden\r\n\r\nprivate", target)
	response, e := http.ReadResponse(bufio.NewReader(connection), nil)
	if e != nil {
		t.Fatal(e)
	}
	defer response.Body.Close()
	body, e := io.ReadAll(response.Body)
	if e != nil {
		t.Fatal(e)
	}
	return fmt.Sprintf("%d|%s|%s", response.StatusCode, response.Header.Get("X-Proxy-Error"), body)
}
func socksRound(t *testing.T, port int, host string, destination int) []byte {
	t.Helper()
	c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(3 * time.Second))
	c.Write([]byte{5, 1, 0})
	greeting := make([]byte, 2)
	if _, e = io.ReadFull(c, greeting); e != nil {
		t.Fatal(e)
	}
	if !bytes.Equal(greeting, []byte{5, 0}) {
		t.Fatal("noauth greeting", greeting)
	}
	packet := append([]byte{5, 1, 0, 3, byte(len(host))}, []byte(host)...)
	packet = append(packet, byte(destination>>8), byte(destination))
	c.Write(packet)
	status := make([]byte, 10)
	if _, e = io.ReadFull(c, status); e != nil {
		t.Fatal(e)
	}
	if status[1] == 0 {
		c.Write([]byte("private SOCKS bytes"))
		reply := make([]byte, len("private SOCKS bytes"))
		if _, e = io.ReadFull(c, reply); e != nil || string(reply) != "private SOCKS bytes" {
			t.Fatal(string(reply), e)
		}
	}
	return status
}
func TestActualSourceSandboxHTTPAndSOCKSPhysicalPolicyUpdateAndJoin(t *testing.T) {
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		fmt.Fprintf(w, "%s|%s|%s|%s|%s", r.Method, r.URL.RequestURI(), r.Header.Get("X-Original"), r.Header.Get("X-Drop"), body)
	}))
	defer peer.Close()
	echo, e := net.Listen("tcp", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	defer echo.Close()
	echoDone := make(chan struct{})
	go func() {
		defer close(echoDone)
		for {
			c, e := echo.Accept()
			if e != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(c, c) }()
		}
	}()
	config := NetworkConfig{AllowedDomains: []string{"127.0.0.1"}, DeniedDomains: []string{"denied.test"}}
	native, e := NewNetwork(context.Background(), config, NetworkOptions{Environment: map[string]string{}})
	if e != nil {
		t.Fatal(e)
	}
	defer native.Close(context.Background())
	np, ns := native.Ports()
	home := t.TempDir()
	ready, release, updated, finish := filepath.Join(home, "ready"), filepath.Join(home, "release"), filepath.Join(home, "updated"), filepath.Join(home, "finish")
	fixture, _ := filepath.Abs("testdata/network-io.fixture.ts")
	data, _ := json.Marshal(map[string]any{"config": config, "ready": ready, "release": release, "updated": updated, "finish": finish, "update": NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}}})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
	cmd.Stdin = bytes.NewReader(append(data, '\n'))
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if e = cmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		os.WriteFile(finish, []byte("end"), 0600)
		os.WriteFile(release, []byte("end"), 0600)
		cmd.Process.Kill()
	}()
	var ports struct{ HTTP, SOCKS int }
	if e = json.Unmarshal(waitFixturePath(t, ready), &ports); e != nil {
		t.Fatal(e)
	}
	target := peer.URL + "/original?x=1"
	goResult := requestRawProxy(t, int(np), target)
	original := requestRawProxy(t, ports.HTTP, target)
	if goResult != original {
		t.Fatal("actual original HTTP mismatch", original, goResult)
	}
	denied := requestRawProxy(t, int(np), "http://denied.test/original")
	sourceDenied := requestRawProxy(t, ports.HTTP, "http://denied.test/original")
	if denied != sourceDenied || !strings.HasPrefix(denied, "403|blocked-by-allowlist|") {
		t.Fatal(sourceDenied, denied)
	}
	destination := echo.Addr().(*net.TCPAddr).Port
	actual := socksRound(t, int(ns), "127.0.0.1", destination)
	source := socksRound(t, ports.SOCKS, "127.0.0.1", destination)
	if !reflect.DeepEqual(actual, source) {
		t.Fatal(actual, source)
	}
	actual = socksRound(t, int(ns), "denied.test", destination)
	source = socksRound(t, ports.SOCKS, "denied.test", destination)
	if !reflect.DeepEqual(actual, source) || actual[1] != 2 {
		t.Fatal(actual, source)
	}
	native.Update(NetworkConfig{AllowedDomains: []string{}, DeniedDomains: []string{}})
	os.WriteFile(release, []byte("update"), 0600)
	waitFixturePath(t, updated)
	goResult = requestRawProxy(t, int(np), target)
	original = requestRawProxy(t, ports.HTTP, target)
	if goResult != original || !strings.HasPrefix(goResult, "403|") {
		t.Fatal("live policy refresh", original, goResult)
	}
	os.WriteFile(finish, []byte("end"), 0600)
	if e = cmd.Wait(); e != nil {
		t.Fatal(e, stderr.String())
	}
	if e = native.Close(context.Background()); e != nil {
		t.Fatal(e)
	}
	for _, port := range []int{int(np), int(ns), ports.HTTP, ports.SOCKS} {
		c, e := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)), time.Second)
		if e == nil {
			c.Close()
			t.Fatal("closed proxy still accepts", port)
		}
	}
	echo.Close()
	<-echoDone
}
