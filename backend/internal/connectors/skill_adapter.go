package connectors

import (
	"context"
	"errors"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
)

type BundleFile struct {
	Target string
	Bytes  []byte
}

var recipeID = regexp.MustCompile(`^[a-z][a-z0-9-]*$`)
var recipeVersion = regexp.MustCompile(`^\d+\.\d+\.\d+$`)
var repositoryName = regexp.MustCompile(`^[\w.-]+/[\w.-]+$`)
var commitHash = regexp.MustCompile(`^[a-f0-9]{40}$`)
var memberPath = regexp.MustCompile(`^[A-Za-z0-9_./ -]+$`)
var sha256Pin = regexp.MustCompile(`^sha256-[a-f0-9]{64}$`)
var skillEntry = regexp.MustCompile(`^skills/[^/]+/SKILL.md$`)

func validateSkillRecipe(recipe SkillRecipe) error {
	if !recipeID.MatchString(recipe.ID) || !recipeVersion.MatchString(recipe.Version) || !repositoryName.MatchString(recipe.Repository) || !commitHash.MatchString(recipe.Commit) {
		return errors.New("Invalid skill bundle identity")
	}
	if len(recipe.Files) == 0 || len(recipe.Files) > 600 {
		return errors.New("Invalid skill bundle size")
	}
	targets := map[string]bool{}
	entry := false
	valid := func(path string) bool {
		if !memberPath.MatchString(path) || strings.HasPrefix(path, "/") {
			return false
		}
		for _, part := range strings.Split(path, "/") {
			if part == "" || part == "." || part == ".." {
				return false
			}
		}
		return true
	}
	for _, file := range recipe.Files {
		if !valid(file.Source) || !valid(file.Target) || !strings.HasPrefix(file.Target, "skills/") && !strings.HasPrefix(file.Target, "LICENSE") || !sha256Pin.MatchString(file.Integrity) || targets[file.Target] {
			return errors.New("Invalid skill bundle file")
		}
		targets[file.Target] = true
		entry = entry || skillEntry.MatchString(file.Target)
	}
	if !entry {
		return errors.New("Skill bundle has no entry point")
	}
	return nil
}

func ReadSkillBundle(recipe SkillRecipe, dir string) ([]BundleFile, error) {
	if err := validateSkillRecipe(recipe); err != nil {
		return nil, err
	}
	root, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	info, err := os.Lstat(root)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("Invalid skill bundle directory")
	}
	output := []BundleFile{}
	total := 0
	for _, file := range recipe.Files {
		parts := strings.Split(file.Target, "/")
		for count := 1; count <= len(parts); count++ {
			target := filepath.Join(append([]string{root}, parts[:count]...)...)
			info, err := os.Lstat(target)
			if err != nil {
				return nil, err
			}
			if info.Mode()&os.ModeSymlink != 0 || count == len(parts) && !info.Mode().IsRegular() || count < len(parts) && !info.IsDir() {
				return nil, errors.New("Invalid skill bundle member")
			}
			if info.Size() > 8*1024*1024 {
				return nil, errors.New("Skill bundle member exceeds size limit")
			}
		}
		bytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file.Target)))
		if err != nil {
			return nil, err
		}
		total += len(bytes)
		if total > 32*1024*1024 {
			return nil, errors.New("Skill bundle exceeds size limit")
		}
		if err := VerifyIntegrity(bytes, file.Integrity); err != nil {
			return nil, err
		}
		output = append(output, BundleFile{file.Target, bytes})
	}
	return output, nil
}

type skillAdapter struct {
	recipe    SkillRecipe
	directory string
	download  func(context.Context, []string, string) ([]byte, error)
	uuid      func() string
}

func SkillAdapter(def protocol.JSONObject, root string, recipe SkillRecipe, download func(context.Context, []string, string) ([]byte, error), uuid func() string) (Adapter, error) {
	if err := validateSkillRecipe(recipe); err != nil {
		return nil, err
	}
	if def.String("id") != recipe.ID || def.String("version") != recipe.Version || def.String("pluginId") != "office-"+recipe.ID+"@haha-connectors" {
		return nil, errors.New("Invalid managed skill identity")
	}
	if download == nil || uuid == nil {
		return nil, errors.New("managed skill requires download and UUID")
	}
	return &skillAdapter{recipe, filepath.Join(root, "bundles", recipe.ID, recipe.Version), download, uuid}, nil
}

func (a *skillAdapter) installation() Installation {
	return Installation{Directory: a.directory, Command: "", Args: []string{}, Environment: map[string]string{}}
}
func (a *skillAdapter) validate(i Installation) error {
	if filepath.Clean(i.Directory) != a.directory || i.Command != "" || len(i.Args) != 0 || len(i.Environment) != 0 {
		return errors.New("Invalid managed skill installation")
	}
	return nil
}
func (a *skillAdapter) Prepare(ctx context.Context, p Progress) (Installation, error) {
	i := a.installation()
	if err := ctx.Err(); err != nil {
		return i, err
	}
	if _, err := ReadSkillBundle(a.recipe, a.directory); err == nil {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		return i, nil
	}
	if err := ctx.Err(); err != nil {
		return i, err
	}
	stage, backup := a.directory+".stage-"+a.uuid(), a.directory+".backup-"+a.uuid()
	if err := os.MkdirAll(stage, 0700); err != nil {
		return i, err
	}
	defer os.RemoveAll(stage)
	p("downloading", nil)
	total := 0
	for _, file := range a.recipe.Files {
		if err := ctx.Err(); err != nil {
			return i, err
		}
		segments := strings.Split(file.Source, "/")
		for j, v := range segments {
			segments[j] = url.PathEscape(v)
		}
		u := "https://raw.githubusercontent.com/" + a.recipe.Repository + "/" + a.recipe.Commit + "/" + strings.Join(segments, "/")
		bytes, err := a.download(ctx, []string{u}, file.Integrity)
		if err != nil {
			return i, err
		}
		if err = VerifyIntegrity(bytes, file.Integrity); err != nil {
			return i, err
		}
		total += len(bytes)
		if len(bytes) > 8*1024*1024 || total > 32*1024*1024 {
			return i, errors.New("Skill bundle exceeds size limit")
		}
		path := filepath.Join(stage, filepath.FromSlash(file.Target))
		if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			return i, err
		}
		if err = os.WriteFile(path, bytes, 0600); err != nil {
			return i, err
		}
	}
	p("verifying", nil)
	if _, err := ReadSkillBundle(a.recipe, stage); err != nil {
		return i, err
	}
	if err := ctx.Err(); err != nil {
		return i, err
	}
	replaced := false
	if err := os.Rename(a.directory, backup); err == nil {
		replaced = true
	} else if !errors.Is(err, os.ErrNotExist) {
		return i, err
	}
	if err := os.Rename(stage, a.directory); err != nil {
		if replaced {
			if restore := os.Rename(backup, a.directory); restore != nil {
				return i, restore
			}
		}
		return i, err
	}
	if replaced {
		if err := os.RemoveAll(backup); err != nil {
			return i, err
		}
	}
	return i, nil
}
func (a *skillAdapter) Authenticate(ctx context.Context, i Installation, p Progress) error {
	if err := a.validate(i); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := ReadSkillBundle(a.recipe, a.directory)
	return err
}
func (a *skillAdapter) Check(ctx context.Context, i Installation) (Check, error) {
	if err := a.validate(i); err != nil {
		return Check{}, err
	}
	if err := ctx.Err(); err != nil {
		return Check{}, err
	}
	if _, err := ReadSkillBundle(a.recipe, a.directory); err != nil {
		return Check{}, err
	}
	if err := ctx.Err(); err != nil {
		return Check{}, err
	}
	return Check{Authenticated: true, Verification: "local"}, nil
}
func (a *skillAdapter) Deactivate(context.Context) error { return nil }
func (a *skillAdapter) Remove(ctx context.Context, i Installation) error {
	if err := a.validate(i); err != nil {
		return err
	}
	return os.RemoveAll(filepath.Dir(a.directory))
}
