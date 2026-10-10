package sandboxruntime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestPinnedSeccompNativeAssetSourceHashInstallModesAndRepair(t *testing.T) {
	raw, e := seccompAssets.ReadFile("assets/seccomp/manifest.json")
	if e != nil {
		t.Fatal(e)
	}
	var manifest struct {
		Version, License string
		Files            []struct{ Source, Asset, SHA256 string }
	}
	if e = json.Unmarshal(raw, &manifest); e != nil || manifest.Version != "0.0.44" || manifest.License != "Apache-2.0" {
		t.Fatal(e, string(raw))
	}
	for _, architecture := range []string{"arm64", "amd64"} {
		root := t.TempDir()
		bpf, apply, e := InstallSeccompAssets(root, architecture)
		if e != nil {
			t.Fatal(e)
		}
		for _, path := range []string{bpf, apply} {
			name := filepath.Base(path)
			data, e := os.ReadFile(path)
			if e != nil {
				t.Fatal(e)
			}
			embedded, e := seccompAssets.ReadFile("assets/seccomp/" + architecture + "/" + name)
			if e != nil || !bytes.Equal(data, embedded) {
				t.Fatal("native extracted bytes differ", path, e)
			}
			hash := sha256.Sum256(data)
			matched := false
			for _, record := range manifest.Files {
				if record.Asset == "backend/internal/sandboxruntime/assets/seccomp/"+architecture+"/"+name {
					matched = hex.EncodeToString(hash[:]) == record.SHA256
					source, _ := filepath.Abs("../../../" + record.Source)
					original, e := os.ReadFile(source)
					if e != nil || !bytes.Equal(original, data) {
						t.Fatal("actual installed source native bytes", source, e)
					}
				}
			}
			if !matched {
				t.Fatal("pinned hash missing or incorrect", path)
			}
			stat, e := os.Stat(path)
			expected := os.FileMode(0600)
			if name == "apply-seccomp" {
				expected = 0700
			}
			if e != nil || stat.Mode().Perm() != expected {
				t.Fatal("native asset mode", path, e)
			}
			os.WriteFile(path, []byte("damaged disposable cache"), 0600)
		}
		if _, _, e = InstallSeccompAssets(root, architecture); e != nil {
			t.Fatal(e)
		}
	}
	if bpf, apply, e := InstallSeccompAssets(t.TempDir(), "386"); e != nil || bpf != "" || apply != "" {
		t.Fatal("unsupported architecture opened", bpf, apply, e)
	}
}

func TestNativeSeccompAssetSymlinksNeverModifyForeignBytesOrModes(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(root, "outside")
	os.Mkdir(outside, 0700)
	owned := filepath.Join(root, "owned")
	os.Symlink(outside, owned)
	if _, _, e := InstallSeccompAssets(owned, "arm64"); e == nil {
		t.Fatal("symlink asset root accepted")
	}
	if entries, e := os.ReadDir(outside); e != nil || len(entries) != 0 {
		t.Fatal("foreign directory changed", entries, e)
	}
	os.Remove(owned)
	_, apply, e := InstallSeccompAssets(owned, "arm64")
	if e != nil {
		t.Fatal(e)
	}
	foreign := filepath.Join(outside, "foreign-helper")
	raw, _ := os.ReadFile(apply)
	os.WriteFile(foreign, raw, 0600)
	os.Remove(apply)
	os.Symlink(foreign, apply)
	if _, _, e = InstallSeccompAssets(owned, "arm64"); e != nil {
		t.Fatal(e)
	}
	stat, e := os.Lstat(apply)
	if e != nil || !stat.Mode().IsRegular() {
		t.Fatal("asset symlink wasn't atomically replaced", e)
	}
	foreignStat, e := os.Stat(foreign)
	if e != nil || foreignStat.Mode().Perm() != 0600 {
		t.Fatal("foreign helper chmod followed symlink", e)
	}
}
