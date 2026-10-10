package imruntime

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

// Installed Source's current getHttpStream creates Boom.output.statusCode,
// whereas downloadMediaMessage inspects error.status. Record that stock-source
// evidence separately from the requested 404/410 recovery behavior.
func TestWhatsAppActualStockSource404And410StatusBoundary(t *testing.T) {
	for _, status := range []int{404, 410, 403, 500} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Error(w, "private expired", status) }))
			defer peer.Close()
			value := object(whatsAppOriginal(t, "native-whatsapp-media-retry.fixture.ts", map[string]any{"peerURL": peer.URL, "key": base64.StdEncoding.EncodeToString(make([]byte, 32))}))
			t.Logf("actual stock Source %s", raw(value))
			if value.String("error") != "" {
				t.Fatal("fixture failure")
			}
			if string(value.Get("retries")) != "0" || string(object(value.Get("value")).Get("outputStatus")) != fmt.Sprint(status) {
				t.Fatal("stock Source status boundary changed", string(raw(value)))
			}
		})
	}
}

func TestWhatsAppMediaRecoveryRetriesOnly404And410OnceAndWritesOnlySuccess(t *testing.T) {
	key := bytes.Repeat([]byte{13}, 32)
	plain := []byte("owned actual recovery physical content")
	for _, initial := range []int{404, 410, 403, 500} {
		for _, second := range []int{200, 410} {
			t.Run(http.StatusText(initial)+"/"+http.StatusText(second), func(t *testing.T) {
				encrypted := object(whatsAppOriginal(t, "native-whatsapp-sdk-media.fixture.ts", map[string]any{"op": "encrypt", "key": base64.StdEncoding.EncodeToString(key), "data": base64.StdEncoding.EncodeToString(plain), "type": "document"}))
				var gets, retries atomic.Int32
				peer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					n := gets.Add(1)
					status := initial
					if n > 1 {
						status = second
					}
					if status != 200 {
						http.Error(w, "private", status)
					} else {
						w.Write(nodeBase64(encrypted.String("data")))
					}
				}))
				defer peer.Close()
				message := object(raw(map[string]any{"key": map[string]any{"id": "OWNED_RETRY"}, "message": map[string]any{"documentMessage": map[string]any{"url": "https://mmg.whatsapp.net/expired", "mediaKey": base64.StdEncoding.EncodeToString(key), "mimetype": "application/pdf", "fileName": "../owned.pdf"}}}))
				m := WhatsAppMedia{Store: NewAttachmentStore(t.TempDir()), HTTP: peer.Client(), Reupload: func(ctx context.Context, message protocol.JSONObject) (protocol.JSONObject, error) {
					retries.Add(1)
					content := object(message.Get("message"))
					doc := object(content.Get("documentMessage"))
					doc.Set("url", raw(peer.URL+"/fresh"))
					content.Set("documentMessage", raw(doc))
					message.Set("message", raw(content))
					return message, nil
				}}
				// Owned TLS transport changes only the HTTP authority; real URL/path/KDF
				// selection remains in the production Download producer.
				m.HTTP = &http.Client{Transport: whatsAppRecoveryTLSRoute{peer, true}}
				local, e := m.Download(context.Background(), message, "private")
				required := initial == 404 || initial == 410
				expected := int32(0)
				if required {
					expected = 1
				}
				if retries.Load() != expected || gets.Load() != 1+expected {
					t.Fatal("retry decision/count", retries.Load(), gets.Load())
				}
				if required && second == 200 {
					if e != nil || local == nil {
						t.Fatal("recovery failed", e)
					}
					disk, e := os.ReadFile(local.Path)
					if e != nil || !bytes.Equal(disk, plain) || !bytes.Equal(local.Buffer, plain) {
						t.Fatal("physical recovered attachment", e)
					}
				} else if e == nil || local != nil {
					t.Fatal("failed fetch wrote attachment")
				}
			})
		}
	}
}

type whatsAppRecoveryTLSRoute struct {
	peer        *httptest.Server
	fetchStatus bool
}

func (r whatsAppRecoveryTLSRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "https"
	u.Host = r.peer.Listener.Addr().String()
	copy.URL = &u
	response, e := r.peer.Client().Transport.RoundTrip(copy)
	if e != nil {
		return response, e
	}
	if r.fetchStatus && req.URL.Path == "/expired" {
		io.Copy(io.Discard, response.Body)
		response.Body.Close()
		return nil, &WhatsAppMediaFetchStatusError{Status: response.StatusCode, Cause: fmt.Errorf("owned fetch status rejection")}
	}
	return response, nil
}

func TestWhatsAppMediaActualSourceStatusShapesAndRetryDecision(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{31}, 32))
	plain := []byte("actual Source low-level fetch status recovery")
	cipher := object(whatsAppOriginal(t, "native-whatsapp-sdk-media.fixture.ts", map[string]any{"op": "encrypt", "key": key, "type": "image", "data": base64.StdEncoding.EncodeToString(plain)})).String("data")
	for _, mode := range []string{"http-response", "fetch-status"} {
		for _, status := range []int{404, 410, 403, 500} {
			t.Run(mode+"/"+http.StatusText(status), func(t *testing.T) {
				peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path == "/expired" {
						http.Error(w, "owned expired", status)
					} else {
						w.Write(nodeBase64(cipher))
					}
				}))
				defer peer.Close()
				want := object(whatsAppOriginal(t, "native-whatsapp-media-retry.fixture.ts", map[string]any{"peerURL": peer.URL, "key": key, "mode": mode, "status": status}))
				var calls, retries atomic.Int32
				transport := whatsAppMediaFetchRoundTripFunc(func(req *http.Request) (*http.Response, error) {
					calls.Add(1)
					copy := req.Clone(req.Context())
					u := *req.URL
					u.Scheme = "http"
					u.Host = peer.Listener.Addr().String()
					copy.URL = &u
					res, e := peer.Client().Transport.RoundTrip(copy)
					if e != nil {
						return nil, e
					}
					if mode == "fetch-status" && req.URL.Path == "/expired" {
						io.Copy(io.Discard, res.Body)
						res.Body.Close()
						return nil, &WhatsAppMediaFetchStatusError{Status: res.StatusCode, Cause: fmt.Errorf("owned fetch status rejection")}
					}
					return res, nil
				})
				m := WhatsAppMedia{Store: NewAttachmentStore(t.TempDir()), HTTP: &http.Client{Transport: transport}, Reupload: func(ctx context.Context, message protocol.JSONObject) (protocol.JSONObject, error) {
					retries.Add(1)
					content := object(message.Get("message"))
					image := object(content.Get("imageMessage"))
					image.Set("url", raw("https://mmg.whatsapp.net/fresh"))
					content.Set("imageMessage", raw(image))
					message.Set("message", raw(content))
					return message, nil
				}}
				local, e := m.Download(context.Background(), object(raw(map[string]any{"key": map[string]any{"id": "PRIVATE_MEDIA_RETRY", "remoteJid": "111@s.whatsapp.net", "fromMe": false}, "message": map[string]any{"imageMessage": map[string]any{"url": "https://mmg.whatsapp.net/expired", "mediaKey": key, "mimetype": "image/png"}}})), "private")
				if string(want.Get("retries")) != fmt.Sprint(retries.Load()) || string(want.Get("calls")) != fmt.Sprint(calls.Load()) {
					t.Fatal("actual Source retry shape differs", string(raw(want)), retries.Load(), calls.Load())
				}
				value := object(want.Get("value"))
				if value.String("data") != "" {
					if e != nil || local == nil {
						t.Fatal("Source success differed", e)
					}
					disk, e := os.ReadFile(local.Path)
					if e != nil || !bytes.Equal(disk, nodeBase64(value.String("data"))) {
						t.Fatal("physical Source attachment differs", e)
					}
				} else if e == nil || local != nil {
					t.Fatal("Source fetch failure succeeded")
				}
			})
		}
	}
}

type whatsAppMediaFetchRoundTripFunc func(*http.Request) (*http.Response, error)

func (f whatsAppMediaFetchRoundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
