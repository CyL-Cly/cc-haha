package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type ExtractArchive func(context.Context, string, string, string) error

func ownedExecutable(environment map[string]string, name string) (string, error) {
	if filepath.IsAbs(name) {
		return name, nil
	}
	for _, dir := range filepath.SplitList(environment["PATH"]) {
		path := filepath.Join(dir, name)
		if stat, e := os.Stat(path); e == nil && !stat.IsDir() && stat.Mode().Perm()&0111 != 0 {
			return path, nil
		}
	}
	return "", fmt.Errorf("%s: executable file not found in owned PATH", name)
}
func SystemTar(environment map[string]string) ExtractArchive {
	return func(ctx context.Context, cwd, archive, into string) error {
		binary, e := ownedExecutable(environment, "tar")
		if e != nil {
			return &DownloadError{Failure{Reason: "storage", Resource: archive, Message: "The system \"tar\" command is required to unpack the speech runtime"}}
		}
		cmd := exec.CommandContext(ctx, binary, "-xzf", filepath.ToSlash(archive), "-C", filepath.ToSlash(into), "--strip-components=1")
		cmd.Dir = cwd
		cmd.Env = environmentList(environment)
		data, e := cmd.CombinedOutput()
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		if e != nil {
			return &DownloadError{Failure{Reason: "storage", Resource: archive, Message: fmt.Sprintf("Could not unpack %s (tar exit %d): %s", archive, cmd.ProcessState.ExitCode(), strings.TrimSpace(string(data)))}}
		}
		return nil
	}
}
func environmentList(environment map[string]string) []string {
	values := make([]string, 0, len(environment))
	for key, value := range environment {
		values = append(values, key+"="+value)
	}
	return values
}

type InstallOptions struct {
	Layout   Layout
	Items    []Item
	Download DownloadOptions
	Extract  ExtractArchive
}

func InstallAll(ctx context.Context, o InstallOptions, report func(Preparation)) error {
	layout := o.Layout
	total := TotalDownloadBytes(o.Items)
	recorded := verifiedMap(layout)
	base := int64(0)
	for _, item := range o.Items {
		if ctx.Err() != nil {
			return context.Cause(ctx)
		}
		name := ManifestName(layout, item)
		proof := ProofPath(layout, item)
		expected := (*int64)(nil)
		if item.Kind == "file" {
			expected = &item.Asset.Bytes
		}
		current := Preparation{Phase: "downloading", Step: item.Step, Resource: item.Asset.Name, CompletedBytes: ptr(base), TotalBytes: ptr(total)}
		entry, ok := recorded[name]
		if ok && matchesRecord(&entry, proof, expected) {
			base += item.Asset.Bytes
			continue
		}
		if item.Kind == "file" {
			if stat, e := os.Stat(proof); e == nil && stat.Size() == item.Asset.Bytes {
				current.Phase = "verifying"
				report(current)
				digest, e := HashFile(ctx, proof)
				if e != nil {
					return e
				}
				if digest == item.Asset.Hash.Value {
					entry, e := DescribeFile(ctx, name, proof, item.Asset.Hash.Value)
					if e != nil {
						return e
					}
					if e = RecordVerified(layout.Manifest, []VerifiedFile{entry}); e != nil {
						return e
					}
					base += item.Asset.Bytes
					continue
				}
				_ = os.Remove(proof)
			}
		}
		current.Phase = "downloading"
		report(current)
		_, e := DownloadAsset(ctx, item.Asset, DownloadPath(layout, item), o.Download, func(progress DownloadProgress) {
			state := current
			state.CompletedBytes = ptr(base + progress.CompletedBytes)
			state.Source = progress.Source
			if progress.ResumedFromBytes != nil {
				state.ResumedFromBytes = ptr(base + *progress.ResumedFromBytes)
			}
			report(state)
		})
		if e != nil {
			return e
		}
		digest := item.Asset.Hash.Value
		if item.Kind == "package" {
			current.Phase = "verifying"
			current.CompletedBytes = ptr(base + item.Asset.Bytes)
			report(current)
			if e = installPackage(ctx, layout, item, o.Extract); e != nil {
				return e
			}
			digest = ""
		}
		entry, e = DescribeFile(ctx, name, proof, digest)
		if e != nil {
			return e
		}
		if e = RecordVerified(layout.Manifest, []VerifiedFile{entry}); e != nil {
			return e
		}
		base += item.Asset.Bytes
	}
	report(Preparation{Phase: "verifying", Step: "verify", CompletedBytes: ptr(total), TotalBytes: ptr(total)})
	if !IsInstalled(layout, o.Items) {
		return &DownloadError{Failure{Reason: "integrity", Message: "Installed speech files did not pass the final check"}}
	}
	return nil
}
func installPackage(ctx context.Context, layout Layout, item Item, extract ExtractArchive) error {
	if extract == nil {
		return errors.New("voice package extraction requires its actual owned system tar producer")
	}
	staging := filepath.Join(layout.Staging, item.Target)
	if e := os.RemoveAll(staging); e != nil {
		return e
	}
	if e := os.MkdirAll(staging, 0755); e != nil {
		return e
	}
	defer os.RemoveAll(staging)
	archive, _ := filepath.Rel(layout.Base, DownloadPath(layout, item))
	into, _ := filepath.Rel(layout.Base, staging)
	if e := extract(ctx, layout.Base, archive, into); e != nil {
		return e
	}
	stat, e := os.Stat(filepath.Join(staging, item.Marker))
	if e != nil || !stat.Mode().IsRegular() {
		return &DownloadError{Failure{Reason: "integrity", Resource: item.Asset.Name, Message: item.Asset.Name + " does not contain " + item.Marker}}
	}
	destination := ItemPath(layout, item)
	if e = os.RemoveAll(destination); e != nil {
		return e
	}
	if e = os.MkdirAll(filepath.Dir(destination), 0755); e != nil {
		return e
	}
	if e = os.Rename(staging, destination); e != nil {
		return e
	}
	_ = os.Remove(DownloadPath(layout, item))
	return nil
}
func RemoveInstall(layout Layout) error {
	if e := os.Remove(layout.Manifest); e != nil && !errors.Is(e, os.ErrNotExist) {
		return e
	}
	return os.RemoveAll(layout.Base)
}
