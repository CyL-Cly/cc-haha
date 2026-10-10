package pluginsapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/pluginschema"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/storage"
	"github.com/NanmiCoder/cc-haha/backend/internal/webfetch"
)

type MarketplaceOptions struct {
	Home, WorkDir, PluginsDir string
	Environment               map[string]string
	Store                     *storage.Store
	Now                       func() time.Time
	Client                    *http.Client
}
type marketplaceMemo struct {
	catalog protocol.JSONObject
	err     error
}
type MarketplaceResolver struct {
	options MarketplaceOptions
	mu      sync.Mutex
	cached  map[string]marketplaceMemo
}

func NewMarketplaceResolver(o MarketplaceOptions) (*MarketplaceResolver, error) {
	if !filepath.IsAbs(o.Home) || !filepath.IsAbs(o.WorkDir) || !filepath.IsAbs(o.PluginsDir) || o.Environment == nil || o.Store == nil {
		return nil, errors.New("Marketplace fetch requires actual process paths/environment/shared store")
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Client == nil {
		o.Client = &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(r *http.Request, via []*http.Request) error {
			if len(via) >= 21 {
				return errors.New("Maximum number of redirects exceeded")
			}
			return nil
		}}
	}
	return &MarketplaceResolver{options: o, cached: map[string]marketplaceMemo{}}, nil
}
func (r *MarketplaceResolver) Lookup(ctx context.Context, id string) (*MarketplacePlugin, error) {
	name, market := splitID(id)
	if name == "" || market == "" {
		return nil, nil
	}
	// Cache-only Source reads the JSON mapping without validating unrelated
	// entries. A corrupt entry must not invalidate another cached marketplace.
	knownPath := filepath.Join(r.options.PluginsDir, "known_marketplaces.json")
	data, err := lspRead(knownPath)
	if err == nil {
		entry := obj(obj(data).Get(market))
		if entry.String("installLocation") != "" {
			catalog, e := ReadMarketplace(entry.String("installLocation"))
			if e == nil {
				for _, value := range arr(catalog.Get("plugins")) {
					p := obj(value)
					if p.String("name") == name {
						return &MarketplacePlugin{p, entry.String("installLocation")}, nil
					}
				}
				return nil, nil
			}
		}
	}
	known, err := ReadKnownMarketplaces(ctx, r.options.PluginsDir)
	if err != nil {
		return nil, nil
	}
	entry := obj(known.Get(market))
	if entry.String("installLocation") == "" {
		return nil, nil
	}
	source := obj(entry.Get("source"))
	if (source.String("source") == "file" || source.String("source") == "directory") && !filepath.IsAbs(source.String("path")) {
		return nil, nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if memo, ok := r.cached[market]; ok {
		if memo.err != nil {
			return nil, nil
		}
		for _, value := range arr(memo.catalog.Get("plugins")) {
			plugin := obj(value)
			if plugin.String("name") == name {
				return &MarketplacePlugin{plugin, entry.String("installLocation")}, nil
			}
		}
		return nil, nil
	}
	catalog, err := r.Fetch(ctx, source)
	r.cached[market] = marketplaceMemo{catalog, err}
	if err != nil {
		return nil, nil
	}
	err = r.options.Store.WithWriteLock(knownPath, func() error {
		data, e := lspRead(knownPath)
		if e != nil {
			return e
		}
		current := obj(data)
		updated := obj(current.Get(market))
		updated.Set("lastUpdated", raw(r.options.Now().UTC().Format("2006-01-02T15:04:05.000Z")))
		current.Set(market, raw(updated))
		return r.write(knownPath, current)
	})
	r.cached[market] = marketplaceMemo{catalog, err}
	if err != nil {
		return nil, nil
	}
	for _, value := range arr(catalog.Get("plugins")) {
		p := obj(value)
		if p.String("name") == name {
			return &MarketplacePlugin{p, entry.String("installLocation")}, nil
		}
	}
	return nil, nil
}
func (r *MarketplaceResolver) write(path string, value protocol.JSONObject) error {
	data, _ := value.MarshalJSON()
	var pretty bytes.Buffer
	_ = json.Indent(&pretty, data, "", "  ")
	return r.options.Store.AtomicWriteRawJSON(path, "marketplace", pretty.Bytes(), storage.WriteOptions{RetryMissingParent: true})
}
func (r *MarketplaceResolver) Fetch(ctx context.Context, source protocol.JSONObject) (protocol.JSONObject, error) {
	return r.fetch(ctx, source, nil, false)
}
func (r *MarketplaceResolver) FetchWithProgress(ctx context.Context, source protocol.JSONObject, progress func(string)) (protocol.JSONObject, error) {
	return r.fetch(ctx, source, progress, true)
}
func (r *MarketplaceResolver) fetch(ctx context.Context, source protocol.JSONObject, progress func(string), cli bool) (catalog protocol.JSONObject, err error) {
	report := func(message string) {
		if progress != nil {
			progress(message)
		}
	}
	cacheDir := filepath.Join(r.options.PluginsDir, "marketplaces")
	if err = os.MkdirAll(cacheDir, 0777); err != nil {
		return catalog, err
	}
	temporary := filepath.Join(cacheDir, "temp_"+strconv.FormatInt(r.options.Now().UnixMilli(), 10))
	manifest := ""
	local := false
	cleanup := false
	defer func() {
		if err != nil && cleanup && !local {
			_ = os.RemoveAll(temporary)
		}
	}()
	switch source.String("source") {
	case "file":
		local = true
		manifest = source.String("path")
		temporary = filepath.Dir(filepath.Dir(manifest))
	case "directory":
		local = true
		temporary = source.String("path")
		manifest = filepath.Join(temporary, ".claude-plugin", "marketplace.json")
	case "settings":
		temporary = filepath.Join(cacheDir, source.String("name"))
		manifest = filepath.Join(temporary, ".claude-plugin", "marketplace.json")
		owner := source.Get("owner")
		if owner == nil {
			owner = raw(rec(field("name", "settings")))
		}
		value := rec(field("name", source.String("name")), protocol.JSONField{Name: "owner", Value: owner}, protocol.JSONField{Name: "plugins", Value: source.Get("plugins")})
		if err = r.write(manifest, value); err != nil {
			return catalog, err
		}
	case "url":
		temporary += ".json"
		cleanup = true
		manifest = temporary
		if err = r.downloadMarketplaceURL(ctx, source, temporary, progress); err != nil {
			return catalog, err
		}
	case "github", "git":
		cleanup = true
		gitURL := source.String("url")
		if source.String("source") == "github" {
			temporary = filepath.Join(cacheDir, strings.Replace(source.String("repo"), "/", "-", 1))
			sshURL := "git@github.com:" + source.String("repo") + ".git"
			httpsURL := "https://github.com/" + source.String("repo") + ".git"
			first, second := httpsURL, sshURL
			if r.sshConfigured(ctx) {
				first, second = sshURL, httpsURL
			}
			if err = r.gitCache(ctx, first, temporary, source, progress); err != nil {
				_ = os.RemoveAll(temporary)
				err = r.gitCache(ctx, second, temporary, source, progress)
			}
		} else {
			err = r.gitCache(ctx, gitURL, temporary, source, progress)
		}
		if err != nil {
			return catalog, err
		}
		relative := source.String("path")
		if relative == "" {
			relative = ".claude-plugin/marketplace.json"
		}
		manifest = filepath.Join(temporary, relative)
	case "npm":
		return catalog, errors.New("NPM marketplace sources not yet implemented")
	default:
		return catalog, errors.New("Unsupported marketplace source type")
	}
	data, e := lspRead(manifest)
	if e != nil {
		if os.IsNotExist(e) {
			return catalog, fmt.Errorf("Marketplace file not found at %s", manifest)
		}
		if cli {
			var path *os.PathError
			if !errors.As(e, &path) {
				return catalog, fmt.Errorf("Failed to parse marketplace file at %s: Invalid JSON in %s: %s", manifest, manifest, e)
			}
		}
		return catalog, e
	}
	schemas, e := pluginschema.Declarations()
	if e != nil {
		return catalog, e
	}
	valid, issues := pluginschema.Validate(schemas.Marketplace, data)
	if len(issues) > 0 {
		if cli {
			return catalog, fmt.Errorf("Failed to parse marketplace file at %s: Invalid schema: %s %s", manifest, manifest, marketplaceIssues(issues))
		}
		return catalog, fmt.Errorf("Invalid marketplace schema: %s", pluginschema.ErrorJSON(issues, nil))
	}
	catalog = obj(valid)
	final := filepath.Join(cacheDir, catalog.String("name"))
	if !strings.HasPrefix(filepath.Clean(final), filepath.Clean(cacheDir)+string(filepath.Separator)) {
		return protocol.JSONObject{}, errors.New("Marketplace name resolves outside cache directory")
	}
	if temporary != final && !local {
		report("Cleaning up old marketplace cache…")
		if err = os.RemoveAll(final); err != nil {
			return protocol.JSONObject{}, err
		}
		if err = os.Rename(temporary, final); err != nil {
			return protocol.JSONObject{}, err
		}
		cleanup = false
	}
	return catalog, nil
}
func (r *MarketplaceResolver) timeout() time.Duration {
	value := r.options.Environment["CLAUDE_CODE_PLUGIN_GIT_TIMEOUT_MS"]
	end := 0
	for i, ch := range value {
		if i == 0 && (ch == '+' || ch == '-') {
			end = i + 1
			continue
		}
		if ch < '0' || ch > '9' {
			break
		}
		end = i + 1
	}
	if n, err := strconv.Atoi(value[:end]); err == nil && n > 0 {
		return time.Duration(n) * time.Millisecond
	}
	return 120 * time.Second
}
func redactGitURL(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "http" && parsed.Scheme != "https" || parsed.User == nil {
		return value
	}
	user := parsed.User.Username()
	password, hasPassword := parsed.User.Password()
	if user != "" {
		user = "***"
	}
	if hasPassword && password != "" {
		password = "***"
	}
	if hasPassword {
		parsed.User = url.UserPassword(user, password)
	} else {
		parsed.User = url.User(user)
	}
	return parsed.String()
}
func (r *MarketplaceResolver) run(ctx context.Context, name, cwd string, args ...string) (int, string, string) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout())
	defer cancel()
	executable := ""
	for _, directory := range filepath.SplitList(r.options.Environment["PATH"]) {
		if directory == "" {
			directory = r.options.WorkDir
		}
		if !filepath.IsAbs(directory) {
			directory = filepath.Join(r.options.WorkDir, directory)
		}
		for _, suffix := range []string{"", ".exe"} {
			candidate := filepath.Join(directory, name+suffix)
			info, err := os.Stat(candidate)
			if err == nil && info.Mode().IsRegular() {
				executable = candidate
				break
			}
		}
		if executable != "" {
			break
		}
	}
	if executable == "" {
		return 1, "", "executable not found: " + name
	}
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = cwd
	env := map[string]string{}
	for key, value := range r.options.Environment {
		env[key] = value
	}
	env["GIT_TERMINAL_PROMPT"] = "0"
	env["GIT_ASKPASS"] = ""
	env["SSH_ASKPASS"] = ""
	keys := []string{}
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	command.Env = []string{}
	for _, key := range keys {
		command.Env = append(command.Env, key+"="+env[key])
	}
	var out, stderr bytes.Buffer
	command.Stdout = &out
	command.Stderr = &stderr
	err := command.Run()
	if err == nil {
		return 0, out.String(), stderr.String()
	}
	code := 1
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		code = exit.ExitCode()
	}
	return code, out.String(), stderr.String()
}
func (r *MarketplaceResolver) sshConfigured(ctx context.Context) bool {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	code, out, stderr := r.run(ctx, "ssh", r.options.WorkDir, "-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=2", "-o", "StrictHostKeyChecking=yes", "git@github.com")
	return code == 1 && (strings.Contains(stderr, "successfully authenticated") || strings.Contains(out, "successfully authenticated"))
}
func (r *MarketplaceResolver) gitCache(ctx context.Context, gitURL, target string, source protocol.JSONObject, progress ...func(string)) error {
	report := func(message string) {
		if len(progress) > 0 && progress[0] != nil {
			progress[0](message)
		}
	}
	report(fmt.Sprintf("Refreshing marketplace cache (timeout: %.0fs)…", r.timeout().Seconds()))
	sparse := []string{}
	for _, value := range arr(source.Get("sparsePaths")) {
		sparse = append(sparse, stringValue(value))
	}
	ref := source.String("ref")
	if _, err := os.Stat(filepath.Join(target, ".git")); err == nil {
		if len(sparse) > 0 {
			args := append([]string{"sparse-checkout", "set", "--cone", "--"}, sparse...)
			if code, _, _ := r.run(ctx, "git", target, args...); code != 0 {
				goto clone
			}
		} else {
			code, out, _ := r.run(ctx, "git", target, "config", "--get", "core.sparseCheckout")
			if code == 0 && strings.TrimSpace(out) == "true" {
				goto clone
			}
		}
		args := []string{"pull", "origin", "HEAD"}
		if ref != "" {
			args = []string{"fetch", "origin", ref}
			if code, _, _ := r.run(ctx, "git", target, args...); code == 0 {
				_, _, _ = r.run(ctx, "git", target, "checkout", ref)
			}
			args = []string{"pull", "origin", ref}
		}
		if code, _, _ := r.run(ctx, "git", target, args...); code == 0 {
			if len(sparse) == 0 {
				if _, err := os.Stat(filepath.Join(target, ".gitmodules")); err == nil {
					_, _, _ = r.run(ctx, "git", target, "-c", "core.sshCommand=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes", "submodule", "update", "--init", "--recursive", "--depth", "1")
				}
			}
			return nil
		}
	}
clone:
	if _, err := os.Stat(target); err == nil {
		report("Found stale directory, cleaning up and re-cloning…")
	}
	_ = os.RemoveAll(target)
	refMessage := ""
	if ref != "" {
		refMessage = " (ref: " + ref + ")"
	}
	report(fmt.Sprintf("Cloning repository (timeout: %.0fs): %s%s", r.timeout().Seconds(), redactGitURL(gitURL), refMessage))
	args := []string{"-c", "core.sshCommand=ssh -o BatchMode=yes -o StrictHostKeyChecking=yes", "clone", "--depth", "1"}
	if len(sparse) > 0 {
		args = append(args, "--filter=blob:none", "--no-checkout")
	} else {
		args = append(args, "--recurse-submodules", "--shallow-submodules")
	}
	if ref != "" {
		args = append(args, "--branch", ref)
	}
	args = append(args, gitURL, target)
	if code, _, stderr := r.run(ctx, "git", r.options.WorkDir, args...); code != 0 {
		return fmt.Errorf("Failed to clone marketplace repository: %s", strings.ReplaceAll(stderr, gitURL, redactGitURL(gitURL)))
	}
	if len(sparse) > 0 {
		args = append([]string{"sparse-checkout", "set", "--cone", "--"}, sparse...)
		if code, _, stderr := r.run(ctx, "git", target, args...); code != 0 {
			return fmt.Errorf("git sparse-checkout set failed: %s", stderr)
		}
		if code, _, stderr := r.run(ctx, "git", target, "checkout", "HEAD"); code != 0 {
			return fmt.Errorf("git checkout after sparse-checkout failed: %s", stderr)
		}
	}
	report("Clone complete, validating marketplace…")
	return nil
}

func (r *MarketplaceResolver) downloadMarketplaceURL(ctx context.Context, source protocol.JSONObject, target string, progress func(string)) error {
	report := func(message string) {
		if progress != nil {
			progress(message)
		}
	}
	var err error
	report("Downloading marketplace from " + redactGitURL(source.String("url")))
	timeout, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	request, e := http.NewRequestWithContext(timeout, "GET", source.String("url"), nil)
	if e != nil {
		return e
	}
	for _, key := range properties(obj(source.Get("headers"))) {
		request.Header.Set(key, obj(source.Get("headers")).String(key))
	}
	request.Header.Set("User-Agent", "Claude-Code-Plugin-Manager")
	request.Header.Set("Accept", "application/json, text/plain, */*")
	request.Header.Set("Accept-Encoding", "gzip, compress, deflate, br")
	response, e := r.options.Client.Do(request)
	if e != nil {
		return e
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return fmt.Errorf("HTTP %d error while downloading marketplace from %s. The marketplace file may not exist at this URL.\n\nTechnical details: Request failed with status code %d", response.StatusCode, redactGitURL(source.String("url")), response.StatusCode)
	}
	data, e := webfetch.ReadAxiosResponseBody(response)
	if e != nil {
		return e
	}
	report("Validating marketplace data")
	schemas, e := pluginschema.Declarations()
	if e != nil {
		return e
	}
	data = protocol.DecodeJavaScriptUTF8(data)
	data = []byte(strings.TrimPrefix(string(data), "\ufeff"))
	if !json.Valid(data) {
		data = raw(string(data))
	}
	valid, issues := pluginschema.Validate(schemas.Marketplace, data)
	if len(issues) > 0 {
		return fmt.Errorf("Invalid marketplace schema from URL: %s", marketplaceIssues(issues))
	}
	report("Saving marketplace to cache")
	if err = r.write(target, obj(valid)); err != nil {
		return err
	}
	return nil
}

func marketplaceIssues(issues []pluginschema.Issue) string {
	messages := []string{}
	for _, issue := range issues {
		parts := []string{}
		for _, part := range issue.Path {
			parts = append(parts, fmt.Sprint(part))
		}
		messages = append(messages, strings.Join(parts, ".")+": "+issue.Message)
	}
	return strings.Join(messages, ", ")
}
