package attachments_test

import (
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/attachments"
	"github.com/NanmiCoder/cc-haha/backend/internal/messages"
	"os"
	"reflect"
	"testing"
	"time"
)

func TestAutoModeProjectionMatchesActualSourceAndFactoryOrder(t *testing.T) {
	bytes, e := os.ReadFile("testdata/auto_mode_source.json")
	if e != nil {
		t.Fatal(e)
	}
	var rows []struct {
		Original      json.RawMessage
		Before, After string
		Output        []string
		Trace         []map[string]string
	}
	if e = json.Unmarshal(bytes, &rows); e != nil {
		t.Fatal(e)
	}
	if len(rows) != 3 {
		t.Fatal("missing actual Source attachment profiles")
	}
	for _, row := range rows {
		count := 0
		trace := []map[string]string{}
		f, e := messages.NewFactory(func() string {
			count++
			id := fmt.Sprintf("auto-id-%d", count)
			trace = append(trace, map[string]string{"kind": "uuid", "value": id})
			return id
		}, func() time.Time {
			now := time.UnixMilli(1700000000000).UTC()
			trace = append(trace, map[string]string{"kind": "date", "value": now.Format("2006-01-02T15:04:05.000Z")})
			return now
		})
		if e != nil {
			t.Fatal(e)
		}
		result, e := attachments.Project(row.Original, f)
		if e != nil {
			t.Fatal(e)
		}
		out := []string{}
		for _, v := range result {
			b, e := v.MarshalJSON()
			if e != nil {
				t.Fatal(e)
			}
			out = append(out, string(b))
		}
		if !reflect.DeepEqual(out, row.Output) || !reflect.DeepEqual(trace, row.Trace) {
			t.Fatalf("original %s\nSource %v %v\nGo %v %v", row.Original, row.Output, row.Trace, out, trace)
		}
		if row.Before != row.After {
			t.Fatal("Source mutated original attachment")
		}
	}
}
