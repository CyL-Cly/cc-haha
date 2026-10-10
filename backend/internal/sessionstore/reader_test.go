package sessionstore

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPageRetainsWholeRecordsAndMalformedContinuation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	if err := os.WriteFile(path, []byte("{\"uuid\":\"first\",\"future\":9007199254740993}\ninvalid\n{\"uuid\":\"last\",\"text\":\"\\ud800\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(nil)
	limit := 1.0
	first, err := reader.ReadPage(context.Background(), path, PageOptions{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Entries) != 1 || string(first.Entries[0].Entry) != `{"uuid":"last","text":"\ud800"}` || first.Page.NextCursor == nil {
		t.Fatalf("first page: %+v", first)
	}
	second, err := reader.ReadPage(context.Background(), path, PageOptions{Cursor: *first.Page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Entries) != 1 || string(second.Entries[0].Entry) != `{"uuid":"first","future":9007199254740993}` || second.Page.OmittedOversizedEntries != 1 {
		t.Fatalf("continued page: %+v", second)
	}
	if serialized, err := json.Marshal(second); err != nil || !bytes.Contains(serialized, []byte(`"future":9007199254740992`)) {
		t.Fatal(err)
	}
}

func TestActualPagesKeepBudgetBoundariesAndBothDirections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	data := `{"id":0}` + "\n" + `{"id":1,"large":"` + strings.Repeat("x", 300*1024) + `"}` + "\n" + `{"id":2,"oversized":"` + strings.Repeat("x", HistorySemanticRecordBytes) + `"}` + "\ninvalid\nnull\n[]\n" + `{"id":3,"bodyTruncated":true,"message":{"content":[{},{}]}}`
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(nil)
	limit := 1.0
	last, err := reader.ReadPage(context.Background(), path, PageOptions{Limit: &limit})
	if err != nil {
		t.Fatal(err)
	}
	if !last.Page.ContentTruncated || last.Page.HistoryComplete {
		t.Fatal("body truncation not reflected")
	}
	previous, err := reader.ReadPage(context.Background(), path, PageOptions{Cursor: *last.Page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(previous.Entries) != 1 || previous.Page.OmittedOversizedEntries != 2 || previous.Page.PreviousCursor == nil {
		t.Fatalf("previous %+v", previous.Page)
	}
	back, err := reader.ReadPage(context.Background(), path, PageOptions{Cursor: *previous.Page.PreviousCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(back.Entries) != 1 || !bytes.Contains(back.Entries[0].Entry, []byte(`"id":3`)) {
		t.Fatalf("forward %+v", back)
	}
	full, err := reader.ReadPage(context.Background(), path, PageOptions{Full: true})
	if err != nil || len(full.Entries) != 3 || full.Page.OmittedOversizedEntries != 2 {
		t.Fatalf("full %+v %v", full.Page, err)
	}
	if _, err = json.Marshal(last); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, []byte("\n\n"), 0600); err != nil {
		t.Fatal(err)
	}
	empty, err := reader.ReadPage(context.Background(), path, PageOptions{})
	if err != nil || len(empty.Entries) != 0 || !empty.Page.HistoryComplete {
		t.Fatal(empty, err)
	}
}

func TestActualScanCeilingContinuesAnOversizedLineWithoutCountingTwice(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.jsonl")
	data := `{"head":true}` + "\n" + strings.Repeat("x", HistoryScanBytes+65536) + "\n"
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	reader := NewReader(nil)
	page, err := reader.ReadPage(context.Background(), path, PageOptions{})
	if err != nil || page.Page.NextCursor == nil || page.Page.OmittedOversizedEntries != 1 || page.Page.ScannedBytes > HistoryScanBytes {
		t.Fatalf("scan page %+v %v", page.Page, err)
	}
	continued, err := reader.ReadPage(context.Background(), path, PageOptions{Cursor: *page.Page.NextCursor})
	if err != nil || len(continued.Entries) != 1 || continued.Page.OmittedOversizedEntries != 0 {
		t.Fatalf("continued %+v %v", continued.Page, err)
	}
	rows := []string{}
	for index := 0; index < 210; index++ {
		rows = append(rows, `{"message":{"content":[{},{},{}]}}`)
	}
	if err = os.WriteFile(path, []byte(strings.Join(rows, "\n")), 0600); err != nil {
		t.Fatal(err)
	}
	bounded, err := reader.ReadPage(context.Background(), path, PageOptions{})
	if err != nil || len(bounded.Entries) != 166 || !bounded.Page.HasMore {
		t.Fatalf("row budget %d %v", len(bounded.Entries), err)
	}
}
