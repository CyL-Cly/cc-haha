package plugincli

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/pluginsapi"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

func ParseMarketplaceInput(input, home, cwd, platform string) (*protocol.JSONObject, error) {
	value := strings.TrimSpace(input)
	build := func(kind, key, value, ref string) *protocol.JSONObject {
		o := record(field("source", kind), field(key, value))
		if ref != "" {
			o.Set("ref", raw(ref))
		}
		return &o
	}
	splitRef := func(s string) (string, string) {
		if i := strings.IndexByte(s, '#'); i >= 0 && i+1 < len(s) {
			return s[:i], s[i+1:]
		}
		return s, ""
	}
	if regexp.MustCompile(`^[a-zA-Z0-9._-]+@[^:]+:.+`).MatchString(value) {
		base, ref := splitRef(value)
		return build("git", "url", base, ref), nil
	}
	if strings.HasPrefix(value, "http://") || strings.HasPrefix(value, "https://") {
		base, ref := splitRef(value)
		if strings.HasSuffix(base, ".git") || strings.Contains(base, "/_git/") {
			return build("git", "url", base, ref), nil
		}
		parsed, err := url.Parse(base)
		if err == nil && (parsed.Hostname() == "github.com" || parsed.Hostname() == "www.github.com") && regexp.MustCompile(`^/[^/]+/[^/]+`).MatchString(parsed.Path) {
			return build("git", "url", base+".git", ref), nil
		}
		return build("url", "url", base, ""), nil
	}
	windows := platform == "windows" && (strings.HasPrefix(value, `.\`) || strings.HasPrefix(value, `..\`) || regexp.MustCompile(`^[a-zA-Z]:[/\\]`).MatchString(value))
	if strings.HasPrefix(value, "./") || strings.HasPrefix(value, "../") || strings.HasPrefix(value, "/") || strings.HasPrefix(value, "~") || windows {
		if strings.HasPrefix(value, "~") {
			value = home + value[1:]
		}
		if !filepath.IsAbs(value) {
			value = filepath.Join(cwd, value)
		}
		value = filepath.Clean(value)
		info, err := os.Stat(value)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("Path does not exist: %s", value)
			}
			return nil, fmt.Errorf("Cannot access path: %s (%s)", value, fileErrno(err))
		}
		if info.IsDir() {
			return build("directory", "path", value, ""), nil
		}
		if info.Mode().IsRegular() {
			if strings.HasSuffix(value, ".json") {
				return build("file", "path", value, ""), nil
			}
			return nil, fmt.Errorf("File path must point to a .json file (marketplace.json), but got: %s", value)
		}
		return nil, fmt.Errorf("Path is neither a file nor a directory: %s", value)
	}
	if strings.Contains(value, "/") && !strings.HasPrefix(value, "@") {
		if strings.Contains(value, ":") {
			return nil, nil
		}
		match := regexp.MustCompile(`^([^#@]+)(?:[#@](.+))?$`).FindStringSubmatch(value)
		base, ref := value, ""
		if len(match) > 0 {
			base = match[1]
			ref = match[2]
		}
		return build("github", "repo", base, ref), nil
	}
	return nil, nil
}
func fileErrno(err error) string {
	if os.IsPermission(err) {
		return "EACCES"
	}
	return err.Error()
}
func RunMarketplace(ctx context.Context, c Command, manager *pluginsapi.MarketplaceManager, home, cwd, platform string, emit ...func(string) int) (Result, error) {
	result := Result{}
	var err error
	progress := func(message string) {
		line := message + "\n"
		result.Stdout += line
		if len(emit) > 0 && emit[0] != nil {
			result.StreamedBytes += emit[0](line)
		}
	}
	switch c.Action {
	case "marketplace-add":
		source, e := ParseMarketplaceInput(c.Argument, home, cwd, platform)
		if e != nil {
			return Result{Stderr: "✘ " + e.Error() + "\n", ExitCode: 1}, nil
		}
		if source == nil {
			return Result{Stderr: "✘ Invalid marketplace source format. Try: owner/repo, https://..., or ./path\n", ExitCode: 1}, nil
		}
		scope := c.Scope
		if scope == "" {
			scope = "user"
		}
		if !installableScope(scope) {
			return Result{Stderr: fmt.Sprintf("✘ Invalid scope '%s'. Use: user, project, or local\n", scope), ExitCode: 1}, nil
		}
		if len(c.Sparse) > 0 {
			kind := source.String("source")
			if kind != "git" && kind != "github" {
				return Result{Stderr: "✘ --sparse is only supported for github and git marketplace sources (got: " + kind + ")\n", ExitCode: 1}, nil
			}
			source.Set("sparsePaths", raw(c.Sparse))
		}
		progress("Adding marketplace...")
		var added pluginsapi.AddedMarketplace
		added, err = manager.Add(ctx, *source, scope, progress)
		if err == nil {
			if added.AlreadyMaterialized {
				progress(fmt.Sprintf("✔ Marketplace '%s' already on disk — declared in %s settings", added.Name, scope))
			} else {
				progress(fmt.Sprintf("✔ Successfully added marketplace: %s (declared in %s settings)", added.Name, scope))
			}
		}
	case "marketplace-list":
		var known protocol.JSONObject
		known, err = manager.Known(ctx)
		if err == nil {
			names := []string{}
			for _, property := range known.OwnProperties() {
				var name string
				_ = json.Unmarshal(property.NameJSON, &name)
				names = append(names, name)
			}
			if c.JSON {
				rows := []protocol.JSONObject{}
				for _, name := range sortedKeys(known) {
					entry := object(known.Get(name))
					source := object(entry.Get("source"))
					row := record(field("name", name))
					if value := source.Get("source"); value != nil {
						row.Set("source", value)
					}
					key := ""
					switch source.String("source") {
					case "github":
						key = "repo"
					case "git", "url":
						key = "url"
					case "directory", "file":
						key = "path"
					}
					if key != "" {
						row.Set(key, source.Get(key))
					}
					if value := entry.Get("installLocation"); value != nil {
						row.Set("installLocation", value)
					}
					rows = append(rows, row)
				}
				progress(pretty(rows))
			} else if len(names) == 0 {
				progress("No marketplaces configured")
			} else {
				progress("Configured marketplaces:\n")
				for _, name := range names {
					entry := object(known.Get(name))
					source := object(entry.Get("source"))
					progress("  ❯ " + name)
					label, key := "", ""
					switch source.String("source") {
					case "github":
						label, key = "GitHub", "repo"
					case "git":
						label, key = "Git", "url"
					case "url":
						label, key = "URL", "url"
					case "directory":
						label, key = "Directory", "path"
					case "file":
						label, key = "File", "path"
					}
					if label != "" {
						progress("    Source: " + label + " (" + source.String(key) + ")")
					}
					progress("")
				}
			}
		}
	case "marketplace-remove":
		err = manager.Remove(ctx, c.Argument)
		if err == nil {
			progress("✔ Successfully removed marketplace: " + c.Argument)
		}
	case "marketplace-update":
		if c.Argument != "" {
			progress("Updating marketplace: " + c.Argument + "...")
			err = manager.Update(ctx, c.Argument, progress)
			if err == nil {
				progress("✔ Successfully updated marketplace: " + c.Argument)
			}
		} else {
			var known protocol.JSONObject
			known, err = manager.Known(ctx)
			if err == nil {
				count := len(known.OwnProperties())
				if count == 0 {
					progress("No marketplaces configured")
				} else {
					progress(fmt.Sprintf("Updating %d marketplace(s)...", count))
					err = manager.UpdateAll(ctx)
					if err == nil {
						progress(fmt.Sprintf("✔ Successfully updated %d marketplace(s)", count))
					}
				}
			}
		}
	default:
		return Result{}, fmt.Errorf("unknown marketplace handler %s", c.Action)
	}
	if err != nil {
		action := map[string]string{"marketplace-add": "add marketplace", "marketplace-list": "list marketplaces", "marketplace-remove": "remove marketplace", "marketplace-update": "update marketplace(s)"}[c.Action]
		result.Stderr = "✘ Failed to " + action + ": " + err.Error() + "\n"
		result.ExitCode = 1
	}
	return result, nil
}
