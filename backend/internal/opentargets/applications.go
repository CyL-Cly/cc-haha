package opentargets

import (
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf16"

	"golang.org/x/text/collate"
	"golang.org/x/text/language"
)

var externalVolume = regexp.MustCompile(`^/Volumes/[^/]+/(?:System/)?Applications/`)

func (s *Service) locationTier(app string) int {
	withSlash := app + "/"
	for _, root := range []string{"/Applications", "/System/Applications", "/System/Library/CoreServices", "/System/Volumes/Preboot/Cryptexes/App/System/Applications", "/System/Cryptexes/App/System/Applications", path.Join(s.options.Home, "Applications")} {
		if strings.HasPrefix(withSlash, root+"/") {
			return 0
		}
	}
	if externalVolume.MatchString(withSlash) {
		return 0
	}
	for _, segment := range strings.Split(app, "/") {
		if strings.HasPrefix(segment, ".") {
			return 2
		}
		switch segment {
		case "Caches", "Application Support", "Library", "node_modules":
			return 2
		}
	}
	return 1
}

var preferredBundles = func() map[string][]string {
	result := map[string][]string{}
	add := func(extensions, bundles []string) {
		for _, ext := range extensions {
			result[ext] = bundles
		}
	}
	add([]string{"pdf", "png", "jpg", "jpeg", "gif", "bmp", "tiff", "webp", "ico"}, []string{"com.apple.Preview"})
	add([]string{"doc", "docx"}, []string{"com.microsoft.Word", "com.apple.iWork.Pages"})
	add([]string{"pages"}, []string{"com.apple.iWork.Pages"})
	add([]string{"xls", "xlsx", "xlsm", "csv"}, []string{"com.microsoft.Excel", "com.apple.iWork.Numbers"})
	add([]string{"numbers"}, []string{"com.apple.iWork.Numbers"})
	add([]string{"ppt", "pptx"}, []string{"com.microsoft.Powerpoint", "com.apple.iWork.Keynote"})
	add([]string{"key"}, []string{"com.apple.iWork.Keynote"})
	add([]string{"zip", "tar", "gz", "bz2", "xz", "7z", "rar"}, []string{"com.apple.archiveutility"})
	add([]string{"mov", "mp4", "m4v", "mp3", "wav", "m4a", "flac"}, []string{"com.apple.QuickTimePlayerX"})
	return result
}()

func bundleRank(a Application, target string) int64 {
	preferred, exists := preferredBundles[strings.ToLower(strings.TrimPrefix(hostExt(target), "."))]
	if !exists {
		return 9007199254740991
	}
	if a.BundleID != nil && *a.BundleID != "" {
		for i, id := range preferred {
			if id == *a.BundleID {
				return int64(i)
			}
		}
	}
	return int64(len(preferred))
}
func (s *Service) preferredCopy(left, right Application) Application {
	canonical := left.CanonicalPath
	if canonical == nil {
		canonical = right.CanonicalPath
	}
	if canonical != nil && *canonical != "" {
		if left.AppPath == *canonical {
			return left
		}
		if right.AppPath == *canonical {
			return right
		}
	}
	if left.IsDefault != right.IsDefault {
		if left.IsDefault {
			return left
		}
		return right
	}
	if l, r := s.locationTier(left.AppPath), s.locationTier(right.AppPath); l != r {
		if l < r {
			return left
		}
		return right
	}
	if l, r := len(strings.Split(left.AppPath, "/")), len(strings.Split(right.AppPath, "/")); l != r {
		if l < r {
			return left
		}
		return right
	}
	if compareUTF16(left.AppPath, right.AppPath) <= 0 {
		return left
	}
	return right
}
func compareUTF16(left, right string) int {
	a, b := utf16.Encode([]rune(left)), utf16.Encode([]rune(right))
	for i := 0; i < len(a) && i < len(b); i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	if len(a) < len(b) {
		return -1
	}
	if len(a) > len(b) {
		return 1
	}
	return 0
}
func (s *Service) discoverApplications(ctx context.Context, target resolvedPath) ([]Application, error) {
	apps := []Application{}
	if s.options.Platform != "darwin" || target.directory {
		return apps, nil
	}
	result, err := s.primitives.ListApplicationsForFile(ctx, target.path)
	if err != nil {
		return nil, err
	}
	static := map[string]bool{}
	for _, d := range s.definitions {
		for _, a := range d.apps["darwin"] {
			static[a] = true
		}
	}
	seen := map[string]int{}
	for _, a := range result.Applications {
		if result.DefaultApplicationPath != nil && a.AppPath == *result.DefaultApplicationPath {
			a.IsDefault = true
		}
		if a.BundleID != nil && *a.BundleID == "com.claude-code-haha.desktop" || strings.Contains(a.AppPath+"/", ".app/Contents/") || static[a.AppPath] {
			continue
		}
		id := applicationID(a)
		if i, exists := seen[id]; exists {
			apps[i] = s.preferredCopy(apps[i], a)
		} else {
			seen[id] = len(apps)
			apps = append(apps, a)
		}
	}
	comparer := collate.New(language.English)
	sort.SliceStable(apps, func(i, j int) bool {
		a, b := apps[i], apps[j]
		if a.IsDefault != b.IsDefault {
			return a.IsDefault
		}
		if l, r := bundleRank(a, target.path), bundleRank(b, target.path); l != r {
			return l < r
		}
		if l, r := s.locationTier(a.AppPath), s.locationTier(b.AppPath); l != r {
			return l < r
		}
		l, r := float64(-1), float64(-1)
		if a.UseCount != nil {
			l = *a.UseCount
		}
		if b.UseCount != nil {
			r = *b.UseCount
		}
		if l != r {
			return l > r
		}
		return comparer.CompareString(a.DisplayName, b.DisplayName) < 0
	})
	if len(apps) > MaxNativeApplications {
		apps = apps[:MaxNativeApplications]
	}
	return apps, nil
}

// ParseApplicationList retains Source's tolerant native-query projection.
func ParseApplicationList(output []byte) ApplicationList {
	result := ApplicationList{Applications: []Application{}}
	var raw map[string]json.RawMessage
	if json.Unmarshal(output, &raw) != nil {
		return result
	}
	if text, ok := jsonString(raw["defaultApplicationPath"]); ok {
		result.DefaultApplicationPath = &text
	}
	var applications []json.RawMessage
	if json.Unmarshal(raw["applications"], &applications) != nil {
		return result
	}
	for _, item := range applications {
		var value map[string]json.RawMessage
		if json.Unmarshal(item, &value) != nil {
			continue
		}
		appPath, pathOK := jsonString(value["appPath"])
		name, nameOK := jsonString(value["displayName"])
		bundle, bundleOK := jsonString(value["bundleId"])
		bundleNull := string(value["bundleId"]) == "null"
		var isDefault bool
		defaultOK := json.Unmarshal(value["isDefault"], &isDefault) == nil && (string(value["isDefault"]) == "true" || string(value["isDefault"]) == "false")
		if !pathOK || !strings.HasPrefix(appPath, "/") || !nameOK || !bundleOK && !bundleNull || !defaultOK {
			continue
		}
		app := Application{AppPath: appPath, DisplayName: name, IsDefault: isDefault}
		if bundleOK {
			app.BundleID = &bundle
		}
		var number float64
		if len(value["useCount"]) > 0 && value["useCount"][0] != '"' && string(value["useCount"]) != "null" && json.Unmarshal(value["useCount"], &number) == nil {
			app.UseCount = &number
		}
		if canonical, ok := jsonString(value["canonicalPath"]); ok {
			app.CanonicalPath = &canonical
		}
		result.Applications = append(result.Applications, app)
	}
	return result
}
