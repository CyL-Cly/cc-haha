package connectors

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestSkillPackageUsesActualFilesIntegrityAndOwnedBoundaries(t *testing.T) {
	root := t.TempDir()
	files := map[string][]byte{"skills/example/SKILL.md": []byte("---\nname: example\n---\nRead supporting files."), "LICENSE": []byte("MIT fixture")}
	var recipe SkillRecipe
	if err := json.Unmarshal(raw(map[string]any{"id": "example", "version": "1.0.0", "repository": "example/repo", "commit": strings.Repeat("a", 40), "license": "MIT", "files": []map[string]string{{"source": "SKILL.md", "target": "skills/example/SKILL.md", "integrity": pin(files["skills/example/SKILL.md"])}, {"source": "LICENSE", "target": "LICENSE", "integrity": pin(files["LICENSE"])}}}), &recipe); err != nil {
		t.Fatal(err)
	}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := "LICENSE"
		if strings.HasSuffix(r.URL.Path, "SKILL.md") {
			name = "skills/example/SKILL.md"
		}
		_, _ = w.Write(files[name])
	}))
	defer peer.Close()
	def := record(field("id", "example"), field("version", "1.0.0"), field("pluginId", "office-example@haha-connectors"))
	download := func(ctx context.Context, urls []string, p string) ([]byte, error) {
		if len(urls) != 1 || !strings.HasPrefix(urls[0], "https://raw.githubusercontent.com/example/repo/"+strings.Repeat("a", 40)+"/") {
			t.Errorf("not a pinned original source path: %v", urls)
		}
		return DownloadVerified(ctx, []string{peer.URL + "/" + filepath.Base(urls[0])}, p)
	}
	adapter, err := SkillAdapter(def, root, recipe, download, func() string { return "fixture" })
	if err != nil {
		t.Fatal(err)
	}
	i, err := adapter.Prepare(context.Background(), func(string, *string) {})
	if err != nil {
		t.Fatal(err)
	}
	checked, err := adapter.Check(context.Background(), i)
	if err != nil || !checked.Authenticated || checked.Verification != "local" {
		t.Fatalf("installed bundle: %+v %v", checked, err)
	}
	foreign := filepath.Join(root, "foreign")
	if err := os.WriteFile(foreign, files["skills/example/SKILL.md"], 0600); err != nil {
		t.Fatal(err)
	}
	member := filepath.Join(i.Directory, "skills", "example", "SKILL.md")
	if runtime.GOOS != "windows" {
		if err := os.Remove(member); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(foreign, member); err != nil {
			t.Fatal(err)
		}
		if _, err := ReadSkillBundle(recipe, i.Directory); err == nil {
			t.Fatal("bundle followed a symlink outside its files")
		}
		if err := os.Remove(member); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(member, []byte("tampered instructions"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := adapter.Check(context.Background(), i); err == nil {
		t.Fatal("tampered Skill was accepted")
	}
	if err := adapter.Remove(context.Background(), Installation{Directory: root, Args: []string{}, Environment: map[string]string{}}); err == nil {
		t.Fatal("foreign installation was accepted")
	}
	if err := adapter.Remove(context.Background(), i); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(i.Directory); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("owned bundle not removed")
	}
	if got, err := os.ReadFile(foreign); err != nil || string(got) != string(files["skills/example/SKILL.md"]) {
		t.Fatal("removal modified a foreign file")
	}
}
