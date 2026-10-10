package clibootstrap

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestNativeMCPOrdinaryImageNotebookGlobGrepPhysicalActualSource(t *testing.T) {
	for _, available := range []bool{false, true} {
		t.Run(fmt.Sprintf("rg-%v", available), func(t *testing.T) { ordinaryFilesSource(t, available) })
	}
}
func ordinaryFilesSource(t *testing.T, available bool) {
	home, env := ordinaryFixture(t)
	env["CLAUDE_CODE_USE_NATIVE_FILE_SEARCH"] = "1"
	if available {
		path, e := exec.LookPath("rg")
		if e != nil {
			t.Fatal("actual rg required", e)
		}
		env["PATH"] = filepath.Dir(path) + ":/usr/bin:/bin"
	}
	files := map[string][]byte{"search.txt": []byte("actual phrase\nsecond phrase\n"), "book.ipynb": []byte(`{"cells":[{"cell_type":"code","id":"owned-cell","source":["print(1)"],"metadata":{},"execution_count":null,"outputs":[]}],"metadata":{"kernelspec":{"language":"python"},"future":{"keep":true}},"nbformat":4,"nbformat_minor":5}`)}
	for i, size := range [][2]int{{12, 8}, {9001, 3}} {
		img := image.NewNRGBA(image.Rect(0, 0, size[0], size[1]))
		for y := 0; y < size[1]; y++ {
			for x := 0; x < size[0]; x++ {
				img.SetNRGBA(x, y, color.NRGBA{uint8(x), uint8(y), uint8(x + y), 137})
			}
		}
		var out bytes.Buffer
		if e := png.Encode(&out, img); e != nil {
			t.Fatal(e)
		}
		name := []string{"small.png", "large.png"}[i]
		files[name] = out.Bytes()
	}
	reset := func() {
		t.Helper()
		for name, data := range files {
			if e := os.WriteFile(filepath.Join(home, name), data, 0600); e != nil {
				t.Fatal(e)
			}
		}
	}
	reset()
	type row struct {
		name string
		args map[string]any
	}
	rows := []row{{"Read", map[string]any{"file_path": filepath.Join(home, "small.png")}}, {"Read", map[string]any{"file_path": filepath.Join(home, "large.png")}}, {"Read", map[string]any{"file_path": filepath.Join(home, "book.ipynb")}}, {"NotebookEdit", map[string]any{"notebook_path": filepath.Join(home, "book.ipynb"), "cell_id": "owned-cell", "new_source": "print(2)", "edit_mode": "replace"}}, {"Read", map[string]any{"file_path": filepath.Join(home, "book.ipynb")}}, {"Glob", map[string]any{"path": home, "pattern": "*.txt"}}, {"Grep", map[string]any{"path": filepath.Join(home, "search.txt"), "pattern": "phrase", "output_mode": "content", "-n": true}}}
	source := startOrdinarySource(t, home, home, env)
	want := make([]any, len(rows))
	for i, row := range rows {
		want[i] = mcpOrdinaryResult(t, source.call(t, row.name, row.args))
	}
	source.close(t)
	sourceNotebook, e := os.ReadFile(filepath.Join(home, "book.ipynb"))
	if e != nil {
		t.Fatal(e)
	}
	reset()
	host, command := ordinaryGoContext(t, home, home, env)
	library, _ := filepath.Abs("../../../node_modules/@img/sharp-libvips-darwin-arm64/lib/libvips-cpp.8.17.3.dylib")
	command.Native.ImageLibraryPath = library
	for i, row := range rows {
		got, e := host.Call(context.Background(), row.name, rawMain(row.args))
		if e != nil {
			t.Fatal(e)
		}
		actual := mcpOrdinaryResult(t, got)
		if !reflect.DeepEqual(actual, want[i]) {
			t.Errorf("%d %s actual Source mismatch GoSHA=%x SourceSHA=%x", i, row.name, sha256.Sum256(rawMain(actual)), sha256.Sum256(rawMain(want[i])))
			if row.name != "Read" {
				t.Logf("Go=%v Source=%v", actual, want[i])
			}
		}
	}
	notebook, e := os.ReadFile(filepath.Join(home, "book.ipynb"))
	if e != nil || !bytes.Equal(notebook, sourceNotebook) {
		t.Fatalf("actual Notebook bytes differ GoSHA=%x SourceSHA=%x %v", sha256.Sum256(notebook), sha256.Sum256(sourceNotebook), e)
	}
}
