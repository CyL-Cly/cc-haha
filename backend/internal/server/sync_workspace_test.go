package server

import (
	"context"
	"encoding/json"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncWorkspaceDelimitedRawUnicodeAndStatEvidence(t *testing.T) {
	options := testOptions(t, func(context.Context, SessionOptions, Emit) (LiveSession, error) { return &transportSession{}, nil })
	_, host := testHost(t, options)
	id := createTestSession(t, host, options.WorkDir)
	base := "/api/sessions/" + id + "/workspace/"
	bytes := []byte("\xef\xbb\xbf名称,内容\n测试,\"一行\n两行\"\n")
	for _, name := range []string{"表.csv", "表.tsv", "Cafe\u0301.txt"} {
		if e := os.WriteFile(filepath.Join(options.WorkDir, name), bytes, 0600); e != nil {
			t.Fatal(e)
		}
	}
	for _, name := range []string{"表.csv", "表.tsv"} {
		raw := requestJSON(t, host, "GET", base+"file?path="+url.QueryEscape(name), "", 200)
		v := historyRecord(raw)
		if stringField(v, "previewType") != "xlsx" || len(v["content"]) != 0 {
			t.Fatal(string(raw))
		}
		res, e := host.Client().Get(host.URL + base + "raw?path=" + url.QueryEscape(name))
		if e != nil {
			t.Fatal(e)
		}
		actual, e := io.ReadAll(res.Body)
		res.Body.Close()
		mime := "text/csv"
		if strings.HasSuffix(name, ".tsv") {
			mime = "text/tab-separated-values"
		}
		if e != nil || string(actual) != string(bytes) || res.Header.Get("Content-Type") != mime {
			t.Fatal(string(actual), res.Header, e)
		}
	}
	raw := requestJSON(t, host, "GET", base+"stat?path="+url.QueryEscape("Café.txt")+"&path=missing&path=../outside&path=missing", "", 200)
	var reply struct {
		Files []struct {
			Path, State string
			MtimeMs     *float64
		}
	}
	if e := json.Unmarshal(raw, &reply); e != nil {
		t.Fatal(e)
	}
	if len(reply.Files) != 3 || reply.Files[0].State != "file" || reply.Files[0].MtimeMs == nil || reply.Files[1].State != "missing" || reply.Files[2].State != "unavailable" {
		t.Fatal(string(raw))
	}
	requestJSON(t, host, "GET", base+"stat", "", 400)
	paths := strings.Repeat("&path=unique", 0)
	for i := 0; i < 21; i++ {
		paths += "&path=" + url.QueryEscape(string(rune('a'+i)))
	}
	requestJSON(t, host, "GET", base+"stat?"+paths, "", 400)
	file, e := os.Create(filepath.Join(options.WorkDir, "large.csv"))
	if e != nil {
		t.Fatal(e)
	}
	_ = file.Truncate(10*1024*1024 + 1)
	file.Close()
	raw = requestJSON(t, host, "GET", base+"file?path=large.csv", "", 200)
	if stringField(historyRecord(raw), "state") != "too_large" {
		t.Fatal(string(raw))
	}
	requestJSON(t, host, "GET", base+"raw?path=large.csv", "", 413)
}
