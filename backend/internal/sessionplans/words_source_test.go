package sessionplans

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestActualSourceWordDictionariesAndBigEndianModulo(t *testing.T) {
	bun := os.Getenv("CC_HAHA_SOURCE_BUN")
	if bun == "" {
		t.Skip("pinned actual Source Bun required")
	}
	home := t.TempDir()
	repo, _ := filepath.Abs("../../..")
	original, e := os.ReadFile(filepath.Join(repo, "src", "utils", "words.ts"))
	if e != nil {
		t.Fatal(e)
	}
	boundary := `import { randomBytes } from 'crypto'`
	if strings.Count(string(original), boundary) != 1 {
		t.Fatal("Source crypto primitive import changed")
	}
	module := filepath.Join(home, "words-source.ts")
	e = os.WriteFile(module, []byte(strings.Replace(string(original), boundary, `const randomBytes = globalThis.fixtureRandomBytes`, 1)), 0600)
	if e != nil {
		t.Fatal(e)
	}
	// Execute the original full module. Only its OS crypto import binding is
	// replaced; the dictionaries, selection and modulo functions stay intact.
	script := `let input=0;globalThis.fixtureRandomBytes=n=>{if(n!==4)throw Error('wrong draw');const b=Buffer.alloc(4);b.writeUInt32BE(input);return b};const words=await import(Bun.argv[2]);const outputs=[];for(const i of [...Array(410).keys(),4294967295]){input=i;outputs.push(words.generateWordSlug())}console.log(JSON.stringify(outputs));`
	file := filepath.Join(home, "source.ts")
	_ = os.WriteFile(file, []byte(script), 0600)
	command := exec.Command(bun, file, module)
	command.Dir = home
	command.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, "config"), "TMPDIR=" + home, "NODE_ENV=test", "PATH=/usr/bin:/bin"}
	output, e := command.CombinedOutput()
	if e != nil {
		t.Fatalf("Source words: %v %s", e, output)
	}
	var expected []string
	if e := json.Unmarshal(output, &expected); e != nil {
		t.Fatalf("Source=%s %v", output, e)
	}
	actual := []string{}
	for i := 0; i < 411; i++ {
		value := uint32(i)
		if i == 410 {
			value = ^uint32(0)
		}
		slug, e := wordSlug(func() (uint32, error) { return value, nil })
		if e != nil {
			t.Fatal(e)
		}
		actual = append(actual, slug)
	}
	if !reflect.DeepEqual(actual, expected) {
		for i := range actual {
			if i >= len(expected) || actual[i] != expected[i] {
				t.Fatalf("Source word row%d=%s Go=%s", i, expected[i], actual[i])
			}
		}
	}
}
