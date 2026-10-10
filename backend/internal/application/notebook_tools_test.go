package application

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/tui"
)

func TestNativeNotebookUsesSharedHarnessRandomAndActualFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	var calls atomic.Int32
	var file, insertedID string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		ordinal := calls.Add(1)
		if ordinal > 1 && (!bytes.Contains(body, []byte(`"type":"tool_result"`)) || bytes.Contains(body, []byte(`"is_error":true`))) {
			t.Errorf("Notebook failed before next actual request: %s", body)
		}
		if ordinal == 3 {
			data, err := os.ReadFile(file)
			var notebook struct {
				Cells []struct {
					ID     string
					Source json.RawMessage
				}
			}
			if err == nil {
				err = json.Unmarshal(data, &notebook)
			}
			if err != nil || len(notebook.Cells) != 2 || string(notebook.Cells[1].Source) != `"# actual native insert"` || notebook.Cells[1].ID == "" {
				t.Errorf("insert did not commit a real randomly identified cell: %s %v", data, err)
			} else {
				insertedID = notebook.Cells[1].ID
				if strings.ContainsAny(insertedID, " \n\r\t") {
					t.Error("invalid generated cell ID")
				}
			}
		}
		switch ordinal {
		case 1, 3:
			response(w, "notebook-read-"+strconv.Itoa(int(ordinal)), "Read", map[string]any{"file_path": file}, "")
		case 2:
			response(w, "notebook-insert", "NotebookEdit", map[string]any{"notebook_path": file, "new_source": "# actual native insert", "cell_type": "markdown", "edit_mode": "insert", "cell_id": "original"}, "")
		case 4:
			response(w, "notebook-replace", "NotebookEdit", map[string]any{"notebook_path": file, "new_source": "# actual native replacement", "cell_id": insertedID}, "")
		case 5:
			data, err := os.ReadFile(file)
			if err != nil || !bytes.Contains(data, []byte("# actual native replacement")) {
				t.Errorf("replace did not commit before next request: %s %v", data, err)
			}
			response(w, "notebook-delete", "NotebookEdit", map[string]any{"notebook_path": file, "new_source": "", "edit_mode": "delete", "cell_id": insertedID}, "")
		case 6:
			response(w, "notebook-done", "", nil, "NOTEBOOK COMPLETE")
		default:
			t.Error("unexpected Notebook request", ordinal)
			http.Error(w, "unexpected", 400)
		}
	}))
	defer provider.Close()
	o := nativeSessionOptions(t, provider.URL)
	o.Arguments.Tools = []string{"Read,NotebookEdit"}
	file = filepath.Join(o.WorkDir, "actual.ipynb")
	if err := os.WriteFile(file, []byte(`{"cells":[{"cell_type":"code","id":"original","source":["print(1)"],"metadata":{"unknown":true},"execution_count":1,"outputs":[]}],"metadata":{"language_info":{"name":"python"}},"nbformat":4,"nbformat_minor":5,"unknown":"retained"}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := NewSession(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close(context.Background())
	if err = s.Submit(ctx, tui.Input{Text: "Read, insert, replace and delete an actual notebook cell."}, nil); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(file)
	var notebook struct {
		Cells   []struct{ ID string }
		Unknown string
	}
	if err == nil {
		err = json.Unmarshal(data, &notebook)
	}
	if err != nil || len(notebook.Cells) != 1 || notebook.Cells[0].ID != "original" || notebook.Unknown != "retained" || calls.Load() != 6 {
		t.Fatalf("final actual notebook/history %s calls=%d error=%v", data, calls.Load(), err)
	}
}
