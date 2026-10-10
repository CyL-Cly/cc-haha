package sandboxruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestActualSourceParentConnectHeaderBoundStatusAndUnparsedFields(t *testing.T) {
	rows := []struct {
		name, response string
		expectedLength int
	}{
		{"latin1-status-space", "HTTP/1.1 200\xa0good\r\n\r\nbytes", 5},
		{"normal", "HTTP/1.1 200 Connection established\r\n\r\n\x00\xffbinary", 8},
		{"ignored-invalid-header", "HTTP/1.0 299 strange\r\nnot a valid HTTP header\r\n\r\nbytes", 5},
		{"non2xx", "HTTP/1.1 407 Auth\r\nContent-Length: 0\r\n\r\n", 0},
		{"invalid-version", "HTTP/2.0 200 yes\r\n\r\n", 0},
		{"wrong-digit-count", "HTTP/1.1 2000 wrong\r\n\r\n", 0},
		{"unterminated", "HTTP/1.1 200 yes\r\nX: " + strings.Repeat("x", 16*1024), 0},
		{"closed-before-headers", "HTTP/1.1 200 yes", 0},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			listener, e := net.Listen("tcp", "127.0.0.1:0")
			if e != nil {
				t.Fatal(e)
			}
			defer listener.Close()
			peerDone := make(chan struct{})
			go func() {
				defer close(peerDone)
				for i := 0; i < 2; i++ {
					c, e := listener.Accept()
					if e != nil {
						return
					}
					c.SetDeadline(time.Now().Add(6 * time.Second))
					in := make([]byte, 2048)
					_, _ = c.Read(in)
					_, _ = io.WriteString(c, row.response)
					if row.name != "closed-before-headers" {
						_, _ = io.Copy(io.Discard, c)
					}
					c.Close()
				}
			}()
			proxy := parseParentProxy(map[string]string{"HTTPS_PROXY": "http://" + listener.Addr().String()})
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			c, failure := openParentTunnel(ctx, proxy.https, "target.owned.test", 443, nil, (&net.Dialer{}).DialContext)
			actual := map[string]string{}
			if failure != nil {
				actual["error"] = failure.Error()
			} else {
				data := make([]byte, row.expectedLength)
				_, e = io.ReadFull(c, data)
				c.Close()
				if e != nil {
					t.Fatal(e)
				}
				actual["hex"] = hex.EncodeToString(data)
			}
			home := t.TempDir()
			fixture, _ := filepath.Abs("testdata/parent-response.fixture.ts")
			input, _ := json.Marshal(map[string]any{"proxy": proxy.https.String(), "length": row.expectedLength})
			cmd := exec.CommandContext(ctx, os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
			cmd.Dir = home
			cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin", "BUN_OPTIONS=--no-env-file"}
			cmd.Stdin = bytes.NewReader(input)
			out, e := cmd.CombinedOutput()
			if e != nil {
				t.Fatal(e, string(out))
			}
			var source map[string]string
			if e = json.Unmarshal(out, &source); e != nil {
				t.Fatal(e, string(out))
			}
			a, _ := json.Marshal(actual)
			b, _ := json.Marshal(source)
			if !bytes.Equal(a, b) {
				t.Fatalf("actual Source %s; native %s", b, a)
			}
			select {
			case <-peerDone:
			case <-ctx.Done():
				t.Fatal("actual parent transport did not close/join")
			}
		})
	}
}
