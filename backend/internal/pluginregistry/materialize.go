package pluginregistry

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"math/big"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type Archive interface {
	Convert(context.Context, string, string) error
	ExtractPlugin(context.Context, string, string) (string, error)
}
type MaterializerOptions struct {
	Directories                  Directories
	CWD                          string
	Environment                  map[string]string
	GitExecutable, NpmExecutable string
	Archive                      Archive
	Installed                    *InstalledRegistry
	Now                          func() time.Time
	RandomFloat                  func() float64
}
type Materializer struct {
	options MaterializerOptions
	creator *Creator
}
type Cached struct {
	Path         string              `json:"path"`
	Manifest     protocol.JSONObject `json:"manifest"`
	GitCommitSHA string              `json:"gitCommitSha,omitempty"`
}
type Prepared struct {
	Path   string
	Errors []protocol.JSONObject
	Skip   bool
}

func NewMaterializer(options MaterializerOptions) (*Materializer, error) {
	if options.Directories.Primary == "" || !filepath.IsAbs(options.CWD) || options.Environment == nil {
		return nil, errors.New("plugin materialization requires explicit directories, cwd and environment")
	}
	if options.GitExecutable == "" {
		options.GitExecutable = "git"
	}
	if options.NpmExecutable == "" {
		options.NpmExecutable = "npm"
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	if options.RandomFloat == nil {
		options.RandomFloat = func() float64 {
			var b [8]byte
			if _, err := rand.Read(b[:]); err != nil {
				panic(err)
			}
			return float64(binary.LittleEndian.Uint64(b[:])>>11) / float64(uint64(1)<<53)
		}
	}
	creator, err := NewCreator(nil)
	if err != nil {
		return nil, err
	}
	return &Materializer{options: options, creator: creator}, nil
}
func (m *Materializer) ioPath(path string) string { return resolveRelative(m.options.CWD, path) }
func (m *Materializer) exists(path string) bool   { _, err := os.Stat(m.ioPath(path)); return err == nil }
func (m *Materializer) zipEnabled() bool {
	return envTruthy(m.options.Environment["CLAUDE_CODE_PLUGIN_USE_ZIP_CACHE"])
}
func (m *Materializer) versioned(id, version string) string {
	return VersionedPathIn(m.options.Directories.Primary, id, version)
}
func (m *Materializer) probe(id, version string, anyVersion bool) string {
	for _, seed := range m.options.Directories.Seeds {
		path := VersionedPathIn(seed, id, version)
		if anyVersion {
			parent := filepath.Dir(VersionedPathIn(seed, id, "_"))
			versions, err := os.ReadDir(m.ioPath(parent))
			if err != nil || len(versions) != 1 {
				continue
			}
			path = filepath.Join(parent, versions[0].Name())
		}
		entries, err := os.ReadDir(m.ioPath(path))
		if err == nil && len(entries) > 0 {
			return path
		}
	}
	return ""
}
func (m *Materializer) CopyToVersioned(ctx context.Context, source, id, version string, entry protocol.JSONObject, marketplaceDir string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	path := m.versioned(id, version)
	zip := path + ".zip"
	if m.zipEnabled() {
		if m.exists(zip) {
			return zip, nil
		}
	} else if m.exists(path) {
		entries, err := os.ReadDir(m.ioPath(path))
		if err != nil {
			return "", err
		}
		if len(entries) > 0 {
			return path, nil
		}
		if err := os.Remove(m.ioPath(path)); err != nil {
			return "", err
		}
	}
	if seed := m.probe(id, version, false); seed != "" {
		return seed, nil
	}
	if err := os.MkdirAll(filepath.Dir(m.ioPath(path)), 0777); err != nil {
		return "", err
	}
	local := jsonKind(entry.Get("source")) == "string" && marketplaceDir != ""
	if local {
		resolved, err := PathWithinBase(m.ioPath(marketplaceDir), entry.String("source"))
		if err != nil {
			return "", err
		}
		source = resolved
	}
	if err := CopyDirectory(ctx, m.ioPath(source), m.ioPath(path)); err != nil {
		var pathErr *os.PathError
		if local && errors.Is(err, os.ErrNotExist) && errors.As(err, &pathErr) && pathErr.Path == source {
			return "", fmt.Errorf("Plugin source directory not found: %s (from entry.source: %s)", source, entry.String("source"))
		}
		return "", err
	}
	if err := os.RemoveAll(filepath.Join(m.ioPath(path), ".git")); err != nil {
		return "", err
	}
	entries, err := os.ReadDir(m.ioPath(path))
	if err != nil {
		return "", err
	}
	if len(entries) == 0 {
		return "", fmt.Errorf("Failed to copy plugin %s to versioned cache: destination is empty after copy", id)
	}
	if m.zipEnabled() {
		if m.options.Archive == nil {
			return "", &CapabilityError{"native ZIP cache conversion"}
		}
		if err := m.options.Archive.Convert(ctx, m.ioPath(path), m.ioPath(zip)); err != nil {
			return "", err
		}
		return zip, nil
	}
	return path, nil
}

func (m *Materializer) TemporaryName(source json.RawMessage) (string, error) {
	prefix := "local"
	if jsonKind(source) != "string" {
		prefix = map[string]string{"npm": "npm", "pip": "pip", "github": "github", "url": "git", "git-subdir": "subdir"}[object(source).String("source")]
		if prefix == "" {
			prefix = "unknown"
		}
	}
	suffix, err := randomRadix36(m.options.RandomFloat())
	if err != nil {
		return "", err
	}
	return "temp_" + prefix + "_" + strconv.FormatInt(m.options.Now().UnixMilli(), 10) + "_" + suffix, nil
}
func randomRadix36(value float64) (string, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 || value >= 1 {
		return "", errors.New("invalid Math.random producer value")
	}
	if value == 0 {
		return "", nil
	}
	exact := new(big.Rat).SetFloat64(value)
	lower := new(big.Rat).Add(exact, new(big.Rat).SetFloat64(math.Nextafter(value, 0)))
	lower.Quo(lower, big.NewRat(2, 1))
	upper := new(big.Rat).Add(exact, new(big.Rat).SetFloat64(math.Nextafter(value, 1)))
	upper.Quo(upper, big.NewRat(2, 1))
	scale := big.NewInt(1)
	for count := 1; count <= 400; count++ {
		scale.Mul(scale, big.NewInt(36))
		scaled := new(big.Rat).Mul(exact, new(big.Rat).SetInt(scale))
		floor := new(big.Int).Quo(scaled.Num(), scaled.Denom())
		fraction := new(big.Rat).Sub(scaled, new(big.Rat).SetInt(floor))
		candidate := new(big.Int).Set(floor)
		if fraction.Cmp(big.NewRat(1, 2)) >= 0 {
			candidate.Add(candidate, big.NewInt(1))
		}
		chosen := new(big.Rat).SetFrac(candidate, new(big.Int).Set(scale))
		if chosen.Cmp(lower) >= 0 && chosen.Cmp(upper) <= 0 {
			digits := candidate.Text(36)
			if len(digits) < count {
				digits = strings.Repeat("0", count-len(digits)) + digits
			}
			if len(digits) > 6 {
				digits = digits[:6]
			}
			return digits, nil
		}
	}
	return "", errors.New("radix-36 conversion failed")
}

func (m *Materializer) Cache(ctx context.Context, source json.RawMessage, fallback protocol.JSONObject) (*Cached, error) {
	cachePath := filepath.Join(m.options.Directories.Primary, "cache")
	if err := os.MkdirAll(m.ioPath(cachePath), 0777); err != nil {
		return nil, err
	}
	tempName, err := m.TemporaryName(source)
	if err != nil {
		return nil, err
	}
	tempPath := filepath.Join(cachePath, tempName)
	sha, err := m.install(ctx, source, tempPath)
	if err != nil {
		_ = os.RemoveAll(m.ioPath(tempPath))
		return nil, err
	}
	manifest := protocol.JSONObject{}
	for _, candidate := range []string{filepath.Join(tempPath, ".claude-plugin", "plugin.json"), filepath.Join(tempPath, "plugin.json")} {
		if !m.exists(candidate) {
			continue
		}
		content, err := m.creator.readJSON(m.ioPath(candidate))
		if err != nil {
			return nil, fmt.Errorf("Plugin has a corrupt manifest file at %s. JSON parse error: %s", candidate, err)
		}
		parsed, issues := pluginschema.Validate(m.creator.Schemas.Manifest, content)
		if len(issues) > 0 {
			parts := []string{}
			for _, issue := range issues {
				keys := []string{}
				for _, key := range issue.Path {
					keys = append(keys, fmt.Sprint(key))
				}
				parts = append(parts, strings.Join(keys, ".")+": "+issue.Message)
			}
			return nil, fmt.Errorf("Plugin has an invalid manifest file at %s. Validation errors: %s", candidate, strings.Join(parts, ", "))
		}
		manifest = object(parsed)
		break
	}
	if !manifest.IsObject() {
		if fallback.IsObject() {
			manifest = fallback.Clone()
		} else {
			kind := object(source).String("source")
			if jsonKind(source) == "string" {
				var text string
				_ = json.Unmarshal(source, &text)
				kind = text
			}
			manifest = protocol.NewJSONObject(field("name", tempName), field("description", "Plugin cached from "+kind))
		}
	}
	finalPath := filepath.Join(cachePath, sanitizeCacheComponent(manifest.String("name"), false, false))
	if m.exists(finalPath) {
		if err := os.RemoveAll(m.ioPath(finalPath)); err != nil {
			return nil, err
		}
	}
	if err := os.Rename(m.ioPath(tempPath), m.ioPath(finalPath)); err != nil {
		return nil, err
	}
	return &Cached{Path: finalPath, Manifest: manifest, GitCommitSHA: sha}, nil
}

func (m *Materializer) Prepare(ctx context.Context, entry protocol.JSONObject, location, id string, installation protocol.JSONObject) (Prepared, error) {
	result := Prepared{Errors: []protocol.JSONObject{}}
	source := entry.Get("source")
	if jsonKind(source) == "string" {
		info, err := os.Stat(m.ioPath(location))
		if err != nil {
			return result, err
		}
		base := location
		if !info.IsDir() {
			base = filepath.Dir(location)
		}
		path := filepath.Join(base, entry.String("source"))
		if !m.exists(path) {
			result.Skip = true
			result.Errors = append(result.Errors, errorRecord("generic-error", id, field("error", "Plugin directory not found at path: "+path+". Check that the marketplace entry has the correct path.")))
			return result, nil
		}
		manifest, _ := m.creator.manifest(filepath.Join(m.ioPath(path), ".claude-plugin", "plugin.json"), entry.String("name"), entry.String("source"))
		version := CalculateVersion(source, manifest, m.ioPath(base), entry.String("version"), "")
		cached, err := m.CopyToVersioned(ctx, path, id, version, entry, base)
		if err != nil {
			result.Path = path
		} else {
			result.Path = cached
		}
	} else {
		provided := entry.String("version")
		if installation.Get("version") != nil {
			provided = installation.String("version")
		}
		version := CalculateVersion(source, protocol.JSONObject{}, "", provided, object(source).String("sha"))
		path := m.versioned(id, version)
		if m.zipEnabled() && m.exists(path+".zip") {
			result.Path = path + ".zip"
		} else if m.exists(path) {
			result.Path = path
		} else {
			if seed := m.probe(id, version, version == "unknown"); seed != "" {
				result.Path = seed
			} else {
				cached, err := m.Cache(ctx, source, entry)
				if err == nil {
					actual := version
					if actual == "unknown" {
						actual = CalculateVersion(source, cached.Manifest, m.ioPath(cached.Path), provided, cached.GitCommitSHA)
					}
					result.Path, err = m.CopyToVersioned(ctx, cached.Path, id, actual, entry, "")
					if err == nil && cached.Path != result.Path {
						err = os.RemoveAll(m.ioPath(cached.Path))
					}
				}
				if err != nil {
					result.Skip = true
					result.Errors = append(result.Errors, errorRecord("generic-error", id, field("error", "Failed to download/cache plugin "+entry.String("name")+": "+err.Error())))
					return result, nil
				}
			}
		}
	}
	if m.zipEnabled() && strings.HasSuffix(result.Path, ".zip") {
		if m.options.Archive == nil {
			return result, &CapabilityError{"native ZIP cache extraction"}
		}
		extracted, err := m.options.Archive.ExtractPlugin(ctx, m.ioPath(result.Path), id)
		if err != nil {
			_ = os.Remove(m.ioPath(result.Path))
			return result, err
		}
		result.Path = extracted
	}
	return result, nil
}

// InstallAndRegister owns the explicit management operation. Full startup
// materialization intentionally does not write an installation record.
func (m *Materializer) InstallAndRegister(ctx context.Context, id string, entry protocol.JSONObject, scope string, project *string, localSource string) (string, error) {
	if m.options.Installed == nil {
		return "", &CapabilityError{"shared installed registry"}
	}
	source := entry.Get("source")
	if jsonKind(source) == "string" && localSource != "" {
		source = raw(localSource)
	}
	cached, err := m.Cache(ctx, source, entry)
	if err != nil {
		return "", err
	}
	gitPath := cached.Path
	if localSource != "" {
		gitPath = localSource
	}
	sha := cached.GitCommitSHA
	if sha == "" {
		sha = ReadGitSHA(m.ioPath(gitPath))
	}
	version := CalculateVersion(entry.Get("source"), cached.Manifest, m.ioPath(gitPath), entry.String("version"), cached.GitCommitSHA)
	versioned := m.versioned(id, version)
	finalPath := cached.Path
	if cached.Path != versioned {
		if err = os.MkdirAll(filepath.Dir(m.ioPath(versioned)), 0777); err != nil {
			return "", err
		}
		if err = os.RemoveAll(m.ioPath(versioned)); err != nil {
			return "", err
		}
		if strings.HasPrefix(versioned, cached.Path+string(filepath.Separator)) {
			var random [4]byte
			if _, err = rand.Read(random[:]); err != nil {
				return "", err
			}
			temp := filepath.Join(filepath.Dir(cached.Path), ".claude-plugin-temp-"+strconv.FormatInt(m.options.Now().UnixMilli(), 10)+"-"+hex.EncodeToString(random[:]))
			if err = os.Rename(m.ioPath(cached.Path), m.ioPath(temp)); err != nil {
				return "", err
			}
			if err = os.MkdirAll(filepath.Dir(m.ioPath(versioned)), 0777); err != nil {
				return "", err
			}
			if err = os.Rename(m.ioPath(temp), m.ioPath(versioned)); err != nil {
				return "", err
			}
		} else if err = os.Rename(m.ioPath(cached.Path), m.ioPath(versioned)); err != nil {
			return "", err
		}
		finalPath = versioned
	}
	if m.zipEnabled() {
		if m.options.Archive == nil {
			return "", &CapabilityError{"native ZIP cache conversion"}
		}
		zip := m.versioned(id, version) + ".zip"
		if err = m.options.Archive.Convert(ctx, m.ioPath(finalPath), m.ioPath(zip)); err != nil {
			return "", err
		}
		finalPath = zip
	}
	now := m.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	metadata := protocol.NewJSONObject(field("version", version), field("installedAt", now), field("lastUpdated", now))
	if sha != "" {
		metadata.Set("gitCommitSha", raw(sha))
	}
	if err = m.options.Installed.Add(ctx, id, scope, finalPath, metadata, project); err != nil {
		return "", err
	}
	return finalPath, nil
}

type commandResult struct {
	Stdout, Stderr string
	Code           int
}

func (m *Materializer) run(ctx context.Context, executable string, args []string, cwd string) commandResult {
	var out, stderr bytes.Buffer
	if !strings.ContainsAny(executable, `/\`) {
		found := ""
		extensions := []string{""}
		if runtime.GOOS == "windows" {
			raw := m.options.Environment["PATHEXT"]
			if raw == "" {
				raw = ".COM;.EXE;.BAT;.CMD"
			}
			extensions = append(extensions, strings.Split(strings.ToLower(raw), ";")...)
		}
		for _, dir := range strings.Split(m.options.Environment["PATH"], string(filepath.ListSeparator)) {
			for _, extension := range extensions {
				candidate := resolveRelative(m.options.CWD, filepath.Join(dir, executable+extension))
				if info, err := os.Stat(candidate); err == nil && !info.IsDir() && (runtime.GOOS == "windows" || info.Mode().Perm()&0111 != 0) {
					found = candidate
					break
				}
			}
			if found != "" {
				break
			}
		}
		if found == "" {
			return commandResult{Stderr: "executable not found: " + executable, Code: 1}
		}
		executable = found
	}
	cmd := exec.CommandContext(ctx, executable, args...)
	cmd.Dir = m.ioPath(cwd)
	cmd.Env = []string{}
	keys := []string{}
	for key := range m.options.Environment {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		cmd.Env = append(cmd.Env, key+"="+m.options.Environment[key])
	}
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		code = 1
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			code = exit.ExitCode()
		} else if stderr.Len() == 0 {
			stderr.WriteString(err.Error())
		}
	}
	return commandResult{out.String(), stderr.String(), code}
}
