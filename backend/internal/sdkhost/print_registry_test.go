package sdkhost

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalRegistryRetainsBuiltinAgentsAndRefusesActiveDiscovery(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	cwd := filepath.Join(root, "workspace")
	os.MkdirAll(home, 0700)
	os.MkdirAll(cwd, 0700)
	o := PrintRegistryOptions{Home: home, CWD: cwd, Stat: os.Stat, ReadDir: os.ReadDir, EntryPoint: "sdk-cli", UserType: "external"}
	s, e := NewPrintRegistry(o)
	if e != nil {
		t.Fatal(e)
	}
	c := s.Catalog()
	if len(c.Agents) != 5 || c.Agents[2] != "Explore" || len(c.Commands) == 0 || s.FullInitParityAccepted() {
		t.Fatal(c)
	}
	p := filepath.Join(cwd, ".claude", "skills")
	os.MkdirAll(p, 0700)
	os.WriteFile(filepath.Join(p, "real.md"), []byte("active"), 0600)
	if _, e = NewPrintRegistry(o); e == nil {
		t.Fatal("active private skills falsely erased")
	}
}
