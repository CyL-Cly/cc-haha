package imagegeneration

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestImagePublicDownloadPinsAllValidatedDNSAndActualTLSCloseCancelTimeout(t *testing.T) {
	var requests atomic.Int32
	opened, closed := make(chan struct{}), make(chan struct{})
	peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Accept") != "image/*" || r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("CDN credential/Accept envelope", r.Header)
		}
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte("actual CDN bytes"))
		case "/empty":
		case "/redirect":
			w.Header().Set("Location", "https://private.invalid/steal")
			w.WriteHeader(302)
		case "/limit":
			w.Header().Set("Content-Length", fmt.Sprint(MaxGeneratedBytes+1))
			w.WriteHeader(200)
		case "/pending":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			close(opened)
			<-r.Context().Done()
			close(closed)
		case "/timeout":
			w.WriteHeader(200)
			w.(http.Flusher).Flush()
			<-r.Context().Done()
		}
	}))
	defer peer.Close()
	roots := x509.NewCertPool()
	roots.AddCert(peer.Certificate())
	var dials atomic.Int32
	options := PublicDownloadOptions{TLSConfig: &tls.Config{RootCAs: roots, ServerName: "example.com"}, Resolve: func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("1.1.1.1")}}, nil
	}, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		dials.Add(1)
		if !strings.HasPrefix(address, "8.8.8.8:") {
			return nil, errors.New("unvalidated DNS address chosen")
		}
		return (&net.Dialer{}).DialContext(ctx, network, peer.Listener.Addr().String())
	}}
	url := "https://cdn.invalid"
	for _, profile := range []struct{ path, want string }{{"/ok", ""}, {"/empty", "The generated image was empty."}, {"/redirect", "Image download failed with HTTP 302; redirects are not allowed."}, {"/limit", "The generated image exceeded the 30 MB size limit."}} {
		data, err := DownloadPublic(context.Background(), url+profile.path, options)
		if profile.want == "" {
			if err != nil || string(data) != "actual CDN bytes" {
				t.Fatal(data, err)
			}
		} else if err == nil || err.Error() != profile.want {
			t.Fatal(profile.path, err)
		}
	}
	before := dials.Load()
	options.Resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}, {IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if _, err := DownloadPublic(context.Background(), url+"/ok", options); err == nil || !strings.Contains(err.Error(), "blocked a non-public IP address") || dials.Load() != before {
		t.Fatal("mixed DNS reached socket", err, dials.Load())
	}
	options.Resolve = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("8.8.8.8")}}, nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := DownloadPublic(ctx, url+"/pending", options); done <- err }()
	select {
	case <-opened:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("CDN did not start")
	}
	cancel()
	select {
	case <-closed:
	case <-time.After(5 * time.Second):
		t.Fatal("canceled CDN physical stream not closed")
	}
	select {
	case err := <-done:
		if err == nil || err.Error() != "Image download was aborted or timed out." {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CDN cancellation not settled")
	}
	options.Timeout = 30 * time.Millisecond
	if _, err := DownloadPublic(context.Background(), url+"/timeout", options); err == nil || err.Error() != "Image download was aborted or timed out." {
		t.Fatal("CDN timeout", err)
	}
	if requests.Load() != 6 {
		t.Fatal("redirect/mixed DNS had extra request", requests.Load())
	}
}
