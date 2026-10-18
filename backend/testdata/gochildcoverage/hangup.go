package gochildcoverage

import (
	"bytes"
	"crypto/sha256"
	"debug/buildinfo"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// This is a test-tagged instrumentation boundary, separate from native
// business shutdown. The strict collector records the actual executable and
// original compiler arguments before it gives an indirect pane this directory.
func HangupDirectory(executable, directory string) (string, error) {
	stage := "physical directory"
	fail := func() (string, error) {
		return "", errors.New("native fixture coverage requires an actual attested atomic binary: " + stage)
	}
	if !filepath.IsAbs(directory) || !filepath.IsAbs(executable) {
		return fail()
	}
	physical, err := filepath.EvalSymlinks(directory)
	if err != nil || physical != filepath.Clean(directory) || filepath.Base(filepath.Dir(physical)) != "raw" || !strings.HasPrefix(filepath.Base(physical), "child-") {
		return fail()
	}
	regular := func(path string) ([]byte, error) {
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			return nil, errors.New("native fixture coverage attestation must be regular")
		}
		return os.ReadFile(path)
	}
	stage = "regular receipt"
	receiptBytes, err := regular(filepath.Join(physical, "receipt.json"))
	if err != nil {
		return fail()
	}
	var receipt struct {
		SchemaVersion              int
		Kind, Binary, BinarySha256 string
		ManifestSha256             string
		BuildArguments             []string
	}
	if json.Unmarshal(receiptBytes, &receipt) != nil || receipt.SchemaVersion != 1 || receipt.Kind != "fresh-cli" && receipt.Kind != "self-test" || len(receipt.BinarySha256) != 64 || receipt.Binary != "bin/"+receipt.BinarySha256 {
		return fail()
	}
	stage = "regular manifest/digest"
	manifestBytes, err := regular(filepath.Join(filepath.Dir(filepath.Dir(physical)), "manifest.json"))
	if err != nil {
		return fail()
	}
	manifestSum := sha256.Sum256(manifestBytes)
	if hex.EncodeToString(manifestSum[:]) != receipt.ManifestSha256 {
		return fail()
	}
	var manifest struct {
		SchemaVersion                  int
		RootDir, ModulePath, Toolchain string
		CoveragePackages               []string
		SourceSnapshot                 []struct{ File, Sha256 string }
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.SchemaVersion != 1 || !filepath.IsAbs(manifest.RootDir) || manifest.ModulePath != "github.com/NanmiCoder/cc-haha/backend" || manifest.Toolchain != "go1.25.7" || len(manifest.CoveragePackages) == 0 || len(manifest.SourceSnapshot) == 0 {
		return fail()
	}
	root, err := filepath.EvalSymlinks(manifest.RootDir)
	if err != nil || root != manifest.RootDir {
		return fail()
	}
	stage = "actual source snapshot"
	for _, row := range manifest.SourceSnapshot {
		path := filepath.Join(root, filepath.FromSlash(row.File))
		relative, err := filepath.Rel(root, path)
		if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
			return fail()
		}
		data, err := regular(path)
		sum := sha256.Sum256(data)
		if err != nil || hex.EncodeToString(sum[:]) != row.Sha256 {
			return fail()
		}
	}
	stage = "actual executable hash/toolchain"
	data, err := os.ReadFile(executable)
	sum := sha256.Sum256(data)
	info, infoErr := buildinfo.ReadFile(executable)
	if err != nil || infoErr != nil || hex.EncodeToString(sum[:]) != receipt.BinarySha256 || info.GoVersion != manifest.Toolchain {
		return fail()
	}
	stage = "actual cover build"
	covered := false
	for _, setting := range info.Settings {
		covered = covered || setting.Key == "-cover" && setting.Value == "true"
	}
	if !covered {
		return fail()
	}
	if receipt.Kind == "fresh-cli" {
		atomic := 0
		for _, arg := range receipt.BuildArguments {
			if arg == "-covermode=atomic" {
				atomic++
			}
		}
		if atomic != 1 {
			return fail()
		}
	}
	// Read the real Go 1.25.7 metadata emitted at process startup. Version 1
	// has a 56-byte header and CounterMode at byte 48 (internal/coverage/defs.go).
	// Reject count/set metadata even if a caller edits its receipt arguments.
	stage = "real atomic metadata"
	metas, err := filepath.Glob(filepath.Join(physical, "covmeta.*"))
	if err != nil || len(metas) != 1 {
		return fail()
	}
	meta, err := regular(metas[0])
	if err != nil || len(meta) < 56 || !bytes.Equal(meta[:4], []byte{0, 'c', 'v', 'm'}) || binary.LittleEndian.Uint32(meta[4:8]) != 1 || binary.LittleEndian.Uint64(meta[8:16]) != uint64(len(meta)) || meta[48] != 3 {
		return fail()
	}
	return physical, nil
}
