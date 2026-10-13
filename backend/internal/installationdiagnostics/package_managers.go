package installationdiagnostics

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"
)

type ProcessResult struct {
	Code           int
	Stdout, Stderr string
}
type RunOptions struct {
	Timeout time.Duration
	UseCWD  bool
	Shell   bool
}
type OSRelease struct {
	ID     string   `json:"id"`
	IDLike []string `json:"idLike"`
}
type PackageOptions struct {
	Platform, Executable, Argv0 string
	ReadFile                    func(string) ([]byte, error)
	Run                         func(context.Context, string, []string, RunOptions) (ProcessResult, error)
}
type ownershipCache struct {
	once  sync.Once
	value bool
}
type packageCache struct {
	once        sync.Once
	releaseOnce sync.Once
	manager     string
	release     *OSRelease
	ownership   [4]ownershipCache
}
type Packages struct {
	options PackageOptions
	cache   *packageCache
}

func NewPackages(options PackageOptions) *Packages {
	return &Packages{options: options, cache: &packageCache{}}
}

var misePath = regexp.MustCompile(`(?i)[/\\]mise[/\\]installs[/\\]`)
var asdfPath = regexp.MustCompile(`(?i)[/\\]\.?asdf[/\\]installs[/\\]`)
var wingetPath = regexp.MustCompile(`(?i)Microsoft[/\\]WinGet[/\\](?:Packages|Links)`)

const jsSpace = `[\x09-\x0d\x20\x{00a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}\x{feff}]`

var osID = regexp.MustCompile(`(?m)^ID=["']?([^` + jsSpace[1:len(jsSpace)-1] + `]+?)["']?` + jsSpace + `*$`)
var osLike = regexp.MustCompile(`(?m)^ID_LIKE=["']?([^\r\n\x{2028}\x{2029}]+?)["']?` + jsSpace + `*$`)

func (p *Packages) executable() string {
	if p.options.Executable != "" {
		return p.options.Executable
	}
	return p.options.Argv0
}
func (p *Packages) OSRelease() *OSRelease {
	p.cache.releaseOnce.Do(func() {
		if p.options.ReadFile == nil {
			return
		}
		data, e := p.options.ReadFile("/etc/os-release")
		if e != nil {
			return
		}
		release := &OSRelease{IDLike: []string{}}
		if m := osID.FindStringSubmatch(string(data)); m != nil {
			release.ID = m[1]
		}
		if m := osLike.FindStringSubmatch(string(data)); m != nil {
			release.IDLike = strings.Split(m[1], " ")
		}
		p.cache.release = release
	})
	if p.cache.release == nil {
		return nil
	}
	return &OSRelease{ID: p.cache.release.ID, IDLike: append([]string{}, p.cache.release.IDLike...)}
}
func (p *Packages) Homebrew() bool {
	return (p.options.Platform == "macos" || p.options.Platform == "linux" || p.options.Platform == "wsl") && strings.Contains(p.executable(), "/Caskroom/")
}
func (p *Packages) Winget() bool {
	return p.options.Platform == "windows" && wingetPath.MatchString(p.executable())
}
func (p *Packages) Mise() bool { return misePath.MatchString(p.executable()) }
func (p *Packages) Asdf() bool { return asdfPath.MatchString(p.executable()) }
func (p *Packages) ownership(ctx context.Context, command string, args, families []string) bool {
	if p.options.Platform != "linux" || p.options.Run == nil {
		return false
	}
	if release := p.OSRelease(); release != nil {
		match := false
		for _, family := range families {
			if release.ID == family {
				match = true
			}
			for _, like := range release.IDLike {
				if like == family {
					match = true
				}
			}
		}
		if !match {
			return false
		}
	}
	result, e := p.options.Run(ctx, command, append(append([]string{}, args...), p.executable()), RunOptions{Timeout: 5 * time.Second, UseCWD: false})
	return e == nil && result.Code == 0 && result.Stdout != ""
}
func (p *Packages) owned(ctx context.Context, index int, command string, args, families []string) bool {
	c := &p.cache.ownership[index]
	c.once.Do(func() { c.value = p.ownership(ctx, command, args, families) })
	return c.value
}
func (p *Packages) Pacman(ctx context.Context) bool {
	return p.owned(ctx, 0, "pacman", []string{"-Qo"}, []string{"arch"})
}
func (p *Packages) Apk(ctx context.Context) bool {
	return p.owned(ctx, 1, "apk", []string{"info", "--who-owns"}, []string{"alpine"})
}
func (p *Packages) Deb(ctx context.Context) bool {
	return p.owned(ctx, 2, "dpkg", []string{"-S"}, []string{"debian"})
}
func (p *Packages) Rpm(ctx context.Context) bool {
	return p.owned(ctx, 3, "rpm", []string{"-qf"}, []string{"fedora", "rhel", "suse"})
}
func (p *Packages) Detect(ctx context.Context) string {
	p.cache.once.Do(func() {
		switch {
		case p.Homebrew():
			p.cache.manager = "homebrew"
		case p.Winget():
			p.cache.manager = "winget"
		case p.Mise():
			p.cache.manager = "mise"
		case p.Asdf():
			p.cache.manager = "asdf"
		case p.Pacman(ctx):
			p.cache.manager = "pacman"
		case p.Apk(ctx):
			p.cache.manager = "apk"
		case p.Deb(ctx):
			p.cache.manager = "deb"
		case p.Rpm(ctx):
			p.cache.manager = "rpm"
		default:
			p.cache.manager = "unknown"
		}
	})
	return p.cache.manager
}
