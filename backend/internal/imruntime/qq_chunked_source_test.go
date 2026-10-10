package imruntime

import (
	"bytes"
	"context"
	"crypto/md5"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type qqChunkRoute struct{ api, cos *httptest.Server }

func (r qqChunkRoute) RoundTrip(req *http.Request) (*http.Response, error) {
	peer := r.api
	if req.URL.Host == "cos.qq.private" {
		peer = r.cos
	} else if req.URL.Host != "bots.qq.com" && req.URL.Host != "api.sgroup.qq.com" {
		return nil, fmt.Errorf("unowned chunk target")
	}
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme = "http"
	if peer == r.cos {
		u.Scheme = "https"
	}
	u.Host = peer.Listener.Addr().String()
	copy.URL = &u
	copy.Header.Set("X-Fixture-Logical-URL", req.URL.String())
	return peer.Client().Transport.RoundTrip(copy)
}
func TestQQActualSDKFiveMiBChunkUploadCacheRetryDailyLimitAndFinish(t *testing.T) {
	for _, scenario := range []string{"success-cache", "daily-limit", "retry-part", "persistent-part", "retry-put", "retry-complete"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			records := []protocol.JSONObject{}
			modeSource := true
			putN, finishN, completeN := 0, 0, 0
			size := 5 * 1024 * 1024
			cos := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				digest := md5.Sum(data)
				mu.Lock()
				putN++
				at := putN
				records = append(records, qqObject(qqField("method", r.Method), qqField("url", r.Header.Get("X-Fixture-Logical-URL")), qqField("size", len(data)), qqField("md5", fmt.Sprintf("%x", digest))))
				mu.Unlock()
				if scenario == "retry-put" && at == 1 {
					w.WriteHeader(500)
					io.WriteString(w, "private-cos-failure")
					return
				}
				w.Header().Set("ETag", "\"private-etag\"")
				w.WriteHeader(200)
			}))
			defer cos.Close()
			api := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				data, _ := io.ReadAll(r.Body)
				var body protocol.JSONObject
				_ = body.UnmarshalJSON(data)
				logical := r.Header.Get("X-Fixture-Logical-URL")
				mu.Lock()
				records = append(records, qqObject(qqField("method", r.Method), qqField("url", logical), qqField("body", body)))
				status := 200
				var response any
				switch {
				case logical == QQTokenURL:
					response = map[string]any{"access_token": "private-token", "expires_in": 7200}
				case strings.HasSuffix(logical, "/upload_prepare"):
					if scenario == "daily-limit" {
						status = 400
						response = map[string]any{"code": 40093002, "message": "private daily limit"}
					} else {
						response = map[string]any{"upload_id": "private-upload", "block_size": size / 2, "parts": []any{map[string]any{"index": 1, "presigned_url": "https://cos.qq.private/one"}, map[string]any{"index": 2, "presigned_url": "https://cos.qq.private/two"}}, "concurrency": 1, "retry_timeout": 2}
					}
				case strings.HasSuffix(logical, "/upload_part_finish"):
					finishN++
					if scenario == "retry-part" && finishN == 1 {
						status = 500
						response = map[string]any{"message": "private-part"}
					} else if scenario == "persistent-part" && finishN <= 3 {
						status = 400
						response = map[string]any{"code": 40093001, "message": "private processing"}
					} else {
						response = map[string]any{}
					}
				case strings.HasSuffix(logical, "/files"):
					completeN++
					if scenario == "retry-complete" && completeN == 1 {
						status = 500
						response = map[string]any{"message": "private-complete"}
					} else {
						response = map[string]any{"file_info": "private-info", "file_uuid": "private-file-id", "ttl": 3600}
					}
				default:
					response = map[string]any{"id": "private-message"}
				}
				mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(status)
				w.Write(raw(response))
			}))
			defer api.Close()
			sends := 1
			if scenario == "success-cache" {
				sends = 2
			}
			source := object(qqOriginal(t, "native-qq-chunked.fixture.ts", map[string]any{"httpURL": api.URL, "cosURL": cos.URL, "size": size, "sends": sends}))
			mu.Lock()
			wantRecords := append([]protocol.JSONObject{}, records...)
			records = []protocol.JSONObject{}
			putN, finishN, completeN = 0, 0, 0
			modeSource = false
			mu.Unlock()
			_ = modeSource
			var clockMu sync.Mutex
			now := int64(100000000)
			delays := []int64{}
			schedule := func(d time.Duration, f func()) func() {
				if d <= 4*time.Second {
					clockMu.Lock()
					now += d.Milliseconds()
					delays = append(delays, d.Milliseconds())
					clockMu.Unlock()
					timer := time.AfterFunc(0, f)
					return func() { timer.Stop() }
				}
				timer := time.AfterFunc(d, f)
				return func() { timer.Stop() }
			}
			client, e := NewQQClient(context.Background(), QQClientOptions{Credentials: qqObject(qqField("appId", "app"), qqField("appSecret", "private-secret")), HTTP: &http.Client{Transport: qqChunkRoute{api, cos}}, Now: func() time.Time { clockMu.Lock(); defer clockMu.Unlock(); return time.UnixMilli(now) }, Random: func() float64 { return 0 }, Schedule: schedule})
			if e != nil {
				t.Fatal(e)
			}
			defer client.Close()
			values := []protocol.JSONObject{}
			for i := 0; i < sends; i++ {
				e := client.SendImage(context.Background(), QQTarget{"c2c", NewText("private-user"), NewText("private-inbound")}, OutboundImage{Buffer: bytes.Repeat([]byte{'a'}, size), MIME: "image/png", Alt: "private-image"})
				v := qqObject(qqField("value", qqObject(qqField("id", "private-message"))))
				if e != nil {
					v = qqObject(qqField("error", e.Error()), qqField("name", "Error"))
					var api *QQAPIError
					if errors.As(e, &api) {
						v.Set("name", raw("ApiError"))
					}
					var daily *QQUploadDailyLimitError
					if errors.As(e, &daily) {
						v.Set("name", raw("UploadDailyLimitExceededError"))
						v.Set("filePath", raw(daily.FilePath))
						v.Set("fileSize", raw(daily.FileSize))
					}
				}
				values = append(values, v)
			}
			exactJSON(t, raw(qqObject(qqField("values", values), qqField("delays", delays))), raw(source), "source chunk results")
			mu.Lock()
			got := raw(records)
			mu.Unlock()
			exactJSON(t, got, raw(wantRecords), "actual Source/Go init/COS/finish/cache packets")
		})
	}
}
