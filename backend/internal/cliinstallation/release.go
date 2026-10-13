// Package cliinstallation installs this application's native Go CLI. The
// original Anthropic distribution contains a different runtime and must never
// replace the Go server selected by this application.
package cliinstallation

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Repository = "NanmiCoder/cc-haha"
const ManifestName = "claude-haha-cli-manifest.json"

var versionPattern = regexp.MustCompile(`^v?\d+\.\d+\.\d+(-\S+)?$`)

type Asset struct {
	Name string `json:"name"`
	URL  string `json:"browser_download_url"`
	Size int64  `json:"size"`
}
type Release struct {
	Tag        string  `json:"tag_name"`
	Draft      bool    `json:"draft"`
	Prerelease bool    `json:"prerelease"`
	Assets     []Asset `json:"assets"`
}
type Manifest struct {
	Version string                   `json:"version"`
	Runtime string                   `json:"runtime"`
	Assets  map[string]ManifestAsset `json:"assets"`
}
type ManifestAsset struct {
	SHA256 string `json:"sha256"`
	Size   int64  `json:"size"`
}
type RepositoryClient struct {
	HTTP    *http.Client
	APIBase string
}

func ValidateTarget(target string) (string, error) {
	if versionPattern.MatchString(target) {
		version := strings.TrimPrefix(target, "v")
		if strings.HasPrefix(version, "99.99.") {
			return "", fmt.Errorf("Version %s is not available for installation. Use 'stable' or 'latest'.", version)
		}
		return version, nil
	}
	if target != "stable" && target != "latest" {
		return "", fmt.Errorf("Invalid channel: %s. Use 'stable' or 'latest'", target)
	}
	return target, nil
}

func BinaryName(platform, arch string) (string, error) {
	if arch == "x64" {
		arch = "amd64"
	}
	if platform == "win32" {
		platform = "windows"
	}
	if (platform != "darwin" && platform != "linux" && platform != "windows") || (arch != "arm64" && arch != "amd64") {
		return "", fmt.Errorf("Unsupported Go CLI target: %s/%s", platform, arch)
	}
	name := "claude-haha-" + platform + "-" + arch
	if platform == "windows" {
		name += ".exe"
	}
	return name, nil
}

func (c RepositoryClient) fetch(ctx context.Context, address string, limit int64) ([]byte, error) {
	u, err := url.Parse(address)
	if err != nil || u.Host == "" || u.Scheme != "https" && u.Scheme != "http" {
		return nil, errors.New("Invalid CLI release URL")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "cc-haha-go-cli-installer")
	client := c.HTTP
	if client == nil {
		client = &http.Client{Timeout: 30 * time.Second}
	}
	response, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("CLI release request failed: HTTP %d", response.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > limit {
		return nil, errors.New("CLI release response exceeds the size limit")
	}
	return body, nil
}

func (c RepositoryClient) Resolve(ctx context.Context, target string) (Release, error) {
	target, err := ValidateTarget(target)
	if err != nil {
		return Release{}, err
	}
	base := strings.TrimRight(c.APIBase, "/")
	if base == "" {
		base = "https://api.github.com/repos/" + Repository
	}
	if target != "stable" && target != "latest" {
		body, err := c.fetch(ctx, base+"/releases/tags/"+url.PathEscape("v"+target), 4<<20)
		if err != nil {
			return Release{}, err
		}
		var release Release
		if err = json.Unmarshal(body, &release); err != nil {
			return Release{}, err
		}
		if release.Draft || release.Tag != "v"+target {
			return Release{}, errors.New("Requested CLI release is not published")
		}
		return release, nil
	}
	// GitHub orders releases by creation date. Skip releases without the Go
	// manifest rather than offering the previous TypeScript distribution.
	body, err := c.fetch(ctx, base+"/releases?per_page=100", 8<<20)
	if err != nil {
		return Release{}, err
	}
	var releases []Release
	if err = json.Unmarshal(body, &releases); err != nil {
		return Release{}, err
	}
	for _, release := range releases {
		if release.Draft || target == "stable" && release.Prerelease {
			continue
		}
		if normalized, err := ValidateTarget(release.Tag); err != nil || normalized == "latest" || normalized == "stable" {
			continue
		}
		for _, asset := range release.Assets {
			if asset.Name == ManifestName {
				return release, nil
			}
		}
	}
	return Release{}, errors.New("No published Go CLI release is available")
}

func (c RepositoryClient) Manifest(ctx context.Context, release Release, name string) (Asset, ManifestAsset, error) {
	var manifestAsset, binary Asset
	for _, asset := range release.Assets {
		if asset.Name == ManifestName {
			if manifestAsset.Name != "" {
				return Asset{}, ManifestAsset{}, errors.New("Duplicate CLI manifest asset")
			}
			manifestAsset = asset
		}
		if asset.Name == name {
			if binary.Name != "" {
				return Asset{}, ManifestAsset{}, errors.New("Duplicate CLI binary asset")
			}
			binary = asset
		}
	}
	if manifestAsset.URL == "" || binary.URL == "" {
		return Asset{}, ManifestAsset{}, fmt.Errorf("Go CLI release %s has no complete asset set for %s", release.Tag, name)
	}
	body, err := c.fetch(ctx, manifestAsset.URL, 1<<20)
	if err != nil {
		return Asset{}, ManifestAsset{}, err
	}
	var manifest Manifest
	if err = json.Unmarshal(body, &manifest); err != nil {
		return Asset{}, ManifestAsset{}, err
	}
	entry, present := manifest.Assets[name]
	if manifest.Runtime != "go" || manifest.Version != strings.TrimPrefix(release.Tag, "v") || !present || !regexp.MustCompile(`^[a-fA-F0-9]{64}$`).MatchString(entry.SHA256) || entry.Size <= 0 || entry.Size > 512<<20 || binary.Size != entry.Size {
		return Asset{}, ManifestAsset{}, errors.New("Go CLI release manifest does not match its binary asset")
	}
	return binary, entry, nil
}
