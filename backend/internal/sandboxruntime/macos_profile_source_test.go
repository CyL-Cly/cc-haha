package sandboxruntime

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestActualSourceMacProfileExactRestrictionsMovesGlobAndProxy(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "repo")
	os.MkdirAll(filepath.Join(cwd, "nested"), 0700)
	os.Symlink(cwd, filepath.Join(cwd, "broader"))
	os.Symlink(filepath.Join(cwd, "nested"), filepath.Join(cwd, "deeper"))
	paths := []string{".", "~", "~/nested", "./relative", "/tmp", "/tmp/owned-absent", "**/.git/hooks/**", "a[broken", "a[abc]", "a**b", "a?b", "regex.(foo)+|$", "broader/file", "deeper/file", "broader/**", "deeper/**"}
	paths = append(paths, "~/", home+"/", cwd+"/", "./nested/", "./missing", cwd+"/missing")
	rows := []map[string]any{}
	options := []MacProfileOptions{}
	for _, read := range []*ReadRestriction{nil, {DenyOnly: []string{}, AllowWithinDeny: []string{}}, {DenyOnly: []string{"/"}, AllowWithinDeny: []string{"~/", "./nested"}}, {DenyOnly: []string{"~/secret/**", "a[broken", "**/private*"}, AllowWithinDeny: []string{"~/secret/allowed"}}} {
		for _, write := range []*WriteRestriction{nil, {AllowOnly: []string{}, DenyWithinAllow: []string{}}, {AllowOnly: []string{".", "~/writable/**", "a[broken"}, DenyWithinAllow: []string{".git/config", "**/custom", "~/secret"}}} {
			for _, network := range []bool{false, true} {
				port := float64(12345)
				zero := float64(0)
				in := MacProfileOptions{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}, Read: read, Write: write, HTTPProxyPort: &port, SOCKSProxyPort: &zero, NeedsNetworkRestriction: network, AllowUnixSockets: []string{"~/rpc.sock", "./socket"}, AllowLocalBinding: true, AllowPty: true, EnableWeakerNetworkIsolation: true, LogTag: "CMD64_OWNED_END_PRIVATE_SBX"}
				row := map[string]any{"needsNetworkRestriction": network, "httpProxyPort": port, "socksProxyPort": zero, "allowUnixSockets": in.AllowUnixSockets, "allowLocalBinding": true, "allowPty": true, "enableWeakerNetworkIsolation": true, "paths": paths}
				if read != nil {
					row["read"] = map[string]any{"denyOnly": read.DenyOnly, "allowWithinDeny": read.AllowWithinDeny}
				}
				if write != nil {
					row["write"] = map[string]any{"allowOnly": write.AllowOnly, "denyWithinAllow": write.DenyWithinAllow}
				}
				options = append(options, in)
				rows = append(rows, row)
			}
		}
	}
	row := map[string]any{"needsNetworkRestriction": true, "allowAllUnixSockets": true, "allowGitConfig": true, "paths": paths}
	rows = append(rows, row)
	options = append(options, MacProfileOptions{Paths: PathContext{Home: home, CWD: cwd, Realpath: filepath.EvalSymlinks}, NeedsNetworkRestriction: true, AllowAllUnixSockets: true, AllowGitConfig: true, LogTag: "CMD64_OWNED_END_PRIVATE_SBX"})
	fixture, _ := filepath.Abs("testdata/macos-profile.fixture.ts")
	raw, _ := json.Marshal(map[string]any{"home": home, "cwd": cwd, "rows": rows})
	cmd := exec.Command(os.Getenv("CC_HAHA_SOURCE_BUN"), "--no-env-file", fixture)
	cmd.Env = []string{"HOME=" + home, "USERPROFILE=" + home, "TMPDIR=" + home, "CLAUDE_CONFIG_DIR=" + filepath.Join(home, ".claude"), "CC_HAHA_FIXTURE_TEMP_ROOT=" + filepath.Dir(home), "BUN_OPTIONS=--no-env-file", "PATH=/usr/bin:/bin"}
	cmd.Dir = home
	cmd.Stdin = bytes.NewReader(raw)
	out, e := cmd.CombinedOutput()
	if e != nil {
		t.Fatal(e, string(out))
	}
	var source []struct {
		Profile                      string
		Normalized, Regex, Mandatory []string
	}
	if e = json.Unmarshal(out, &source); e != nil {
		t.Fatal(e, string(out))
	}
	for i, in := range options {
		profile, e := MacProfile(in)
		if e != nil {
			t.Fatal(e)
		}
		if profile != source[i].Profile {
			t.Fatalf("Source%d Seatbelt profile changed\n%s\nGo\n%s", i, source[i].Profile, profile)
		}
		normalized, regex := []string{}, []string{}
		for _, p := range paths {
			normalized = append(normalized, NormalizePath(p, in.Paths))
			regex = append(regex, GlobRegex(p))
		}
		if !reflect.DeepEqual(normalized, source[i].Normalized) || !reflect.DeepEqual(regex, source[i].Regex) || !reflect.DeepEqual(MandatoryMacDenyPaths(resolvedWorkingDirectory(in.Paths), in.AllowGitConfig), source[i].Mandatory) {
			t.Fatalf("Source%d paths/regex/mandatory changed\nSource%+v\nGo%v %v", i, source[i], normalized, regex)
		}
	}
}
