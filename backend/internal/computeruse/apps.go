package computeruse

import (
	"context"
	"encoding/json"
	"math"
	"math/big"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

type InstalledApp struct {
	BundleID    string `json:"bundleId"`
	DisplayName string `json:"displayName"`
	Path        string `json:"path"`
}

func contained(root, path string) bool {
	relative, err := filepath.Rel(root, path)
	return err == nil && relative != "." && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}
func (service *Service) InstalledApps(ctx context.Context) ([]InstalledApp, error) {
	if service.options.Platform == "win32" {
		raw := service.windowsInstalledApps(ctx)
		apps := []InstalledApp{}
		_ = json.Unmarshal(raw, &apps)
		if apps == nil {
			apps = []InstalledApp{}
		}
		return apps, nil
	}
	apps := []InstalledApp{}
	if service.options.Platform != "darwin" {
		return apps, nil
	}
	seenApps := map[string]bool{}
	for _, configuredRoot := range service.options.AppRoots {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		root, err := filepath.EvalSymlinks(configuredRoot)
		if err != nil {
			continue
		}
		type directory struct {
			path  string
			depth int
		}
		queue := []directory{{root, 0}}
		seen := map[string]bool{}
		entriesSeen := 0
		for len(queue) > 0 && entriesSeen < 10000 {
			next := queue[0]
			queue = queue[1:]
			if seen[next.path] {
				continue
			}
			seen[next.path] = true
			entries, err := os.ReadDir(next.path)
			if err != nil {
				continue
			}
			for _, entry := range entries {
				entriesSeen++
				if entriesSeen > 10000 {
					break
				}
				if entry.Type()&os.ModeSymlink != 0 || !entry.IsDir() {
					continue
				}
				candidate := filepath.Join(next.path, entry.Name())
				if filepath.Dir(candidate) != next.path {
					continue
				}
				canonical, err := filepath.EvalSymlinks(candidate)
				if err != nil || !contained(root, canonical) {
					continue
				}
				if !strings.HasSuffix(entry.Name(), ".app") {
					if next.depth < 6 {
						queue = append(queue, directory{canonical, next.depth + 1})
					}
					continue
				}
				result, err := service.run(ctx, "/usr/bin/plutil", []string{"-convert", "json", "-o", "-", filepath.Join(canonical, "Contents", "Info.plist")}, nil)
				if err != nil || result.Code != 0 {
					continue
				}
				var metadata object
				if json.Unmarshal([]byte(result.Stdout), &metadata) != nil {
					continue
				}
				id, _ := stringValue(metadata["CFBundleIdentifier"])
				id = trim(id)
				if id == "" || seenApps[id] {
					continue
				}
				name := ""
				for _, key := range []string{"CFBundleDisplayName", "CFBundleName"} {
					if value, ok := stringValue(metadata[key]); ok && trim(value) != "" {
						name = trim(value)
						break
					}
				}
				if name == "" {
					name = strings.TrimSuffix(filepath.Base(canonical), ".app")
				}
				seenApps[id] = true
				apps = append(apps, InstalledApp{id, name, canonical})
			}
		}
	}
	// Preserve the Source API's one-shot discovery fallback. Current Swift
	// helpers deny this verb outside their daemon; that rejection yields [].
	// Agent actions never take this transport fallback.
	if len(apps) == 0 {
		binary, err := service.ResolveLaunchableHelper(ctx)
		if err == nil && binary != "" {
			result, callErr := service.run(ctx, binary, []string{"list_installed_apps", "--payload", "{}"}, nil)
			raw, err := parseEnvelope("list_installed_apps", result)
			if callErr != nil && result.Stdout == "" {
				err = callErr
			}
			if err == nil {
				_ = json.Unmarshal(raw, &apps)
				if apps == nil {
					apps = []InstalledApp{}
				}
			}
		}
	}
	comparator := collate.New(language.English)
	sort.SliceStable(apps, func(i, j int) bool {
		result := comparator.CompareString(apps[i].DisplayName, apps[j].DisplayName)
		if result == 0 {
			result = comparator.CompareString(apps[i].BundleID, apps[j].BundleID)
		}
		return result < 0
	})
	return apps, nil
}
func NormalizeIconSize(text string) int {
	if text == "" {
		return 64
	}
	value, err := strconv.ParseFloat(trim(text), 64)
	normalized := trim(text)
	if len(normalized) > 2 && normalized[0] == '0' {
		base := 0
		switch normalized[1] {
		case 'x', 'X':
			base = 16
		case 'o', 'O':
			base = 8
		case 'b', 'B':
			base = 2
		}
		if base != 0 {
			integer, ok := new(big.Int).SetString(normalized[2:], base)
			if ok && !strings.Contains(normalized, "_") {
				value, _ = new(big.Float).SetInt(integer).Float64()
				err = nil
			} else {
				err = strconv.ErrSyntax
			}
		}
	}
	if strings.Contains(normalized, "_") || strings.ContainsAny(normalized, "pP") {
		err = strconv.ErrSyntax
	}
	if trim(text) == "" {
		value, err = 0, nil
	}
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
		return 64
	}
	if value < 16 {
		return 16
	}
	if value > 256 {
		return 256
	}
	size := int(math.Floor(value + 0.5))
	if size < 16 {
		size = 16
	}
	if size > 256 {
		size = 256
	}
	return size
}
func (service *Service) AppIcon(ctx context.Context, bundleID, sizeText string) []byte {
	service.appsMu.Lock()
	defer service.appsMu.Unlock()
	if service.appsAt.IsZero() || time.Since(service.appsAt) > 30*time.Second {
		apps, err := service.InstalledApps(ctx)
		if err != nil {
			return nil
		}
		service.apps, service.appsAt = apps, time.Now()
	}
	appPath := ""
	for _, app := range service.apps {
		if app.BundleID == bundleID {
			appPath = app.Path
		}
	}
	if appPath == "" {
		return nil
	}
	size := NormalizeIconSize(sizeText)
	key := appPath + ":" + strconv.Itoa(size)
	if png := service.icons[key]; png != nil {
		for index, cached := range service.iconKeys {
			if cached == key {
				service.iconKeys = append(service.iconKeys[:index], service.iconKeys[index+1:]...)
				break
			}
		}
		service.iconKeys = append(service.iconKeys, key)
		return append([]byte(nil), png...)
	}
	resources := filepath.Join(appPath, "Contents", "Resources")
	icon := ""
	result, err := service.run(ctx, "/usr/bin/plutil", []string{"-extract", "CFBundleIconFile", "raw", "-o", "-", filepath.Join(appPath, "Contents", "Info.plist")}, nil)
	if err == nil && result.Code == 0 && trim(result.Stdout) != "" {
		name := trim(result.Stdout)
		if filepath.Ext(name) == "" {
			name += ".icns"
		}
		candidate := filepath.Join(resources, name)
		if exists(candidate) {
			icon = candidate
		}
	}
	if icon == "" {
		entries, _ := os.ReadDir(resources)
		for _, entry := range entries {
			name := entry.Name()
			if strings.HasSuffix(strings.ToLower(name), ".icns") && !strings.Contains(strings.ToLower(name), "document") {
				candidate := filepath.Join(resources, name)
				if exists(candidate) {
					icon = candidate
				}
				break
			}
		}
	}
	if icon == "" {
		return nil
	}
	temporary, err := os.MkdirTemp(service.options.TempDir, "cc-haha-cu-app-icon-")
	if err != nil {
		return nil
	}
	defer os.RemoveAll(temporary)
	output := filepath.Join(temporary, "icon.png")
	result, err = service.run(ctx, "/usr/bin/sips", []string{"-z", strconv.Itoa(size), strconv.Itoa(size), "-s", "format", "png", icon, "--out", output}, nil)
	if err != nil || result.Code != 0 {
		return nil
	}
	png, err := os.ReadFile(output)
	if err != nil {
		return nil
	}
	if len(service.icons) >= 512 {
		delete(service.icons, service.iconKeys[0])
		service.iconKeys = service.iconKeys[1:]
	}
	service.icons[key] = append([]byte(nil), png...)
	service.iconKeys = append(service.iconKeys, key)
	return png
}
