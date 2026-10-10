package bootstrapstate

import (
	"path/filepath"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"golang.org/x/text/unicode/norm"
)

type ConfigHost struct {
	Environment func(string) (string, bool)
	HomeDir     func() (string, error)
}
type configKey struct {
	value   string
	present bool
}
type ConfigResolver struct {
	owner *cooperative.Owner
	host  ConfigHost
	cache map[configKey]string
}

// Cache keys are the original env value, including undefined versus empty.
// NFC applies to the result, after fallback resolution; HOME is not a key.
func NewConfigResolver(owner *cooperative.Owner, host ConfigHost) (*ConfigResolver, error) {
	if owner == nil || host.Environment == nil || host.HomeDir == nil {
		return nil, cooperative.ErrRestricted
	}
	return &ConfigResolver{owner: owner, host: host, cache: map[configKey]string{}}, nil
}
func (resolver *ConfigResolver) ConfigHome(scope *cooperative.Scope) (string, error) {
	if resolver == nil {
		return "", cooperative.ErrRestricted
	}
	if err := check(scope, resolver.owner); err != nil {
		return "", err
	}
	value, present := resolver.host.Environment("CLAUDE_CONFIG_DIR")
	key := configKey{value, present}
	if cached, ok := resolver.cache[key]; ok {
		return cached, nil
	}
	if !present {
		home, err := resolver.host.HomeDir()
		if err != nil {
			return "", err
		}
		if err := valid(home); err != nil {
			return "", err
		}
		value = filepath.Join(home, ".claude")
	}
	if err := valid(value); err != nil {
		return "", err
	}
	value = norm.NFC.String(value)
	resolver.cache[key] = value
	return value, nil
}
func (resolver *ConfigResolver) CcHahaDir(scope *cooperative.Scope) (string, error) {
	home, err := resolver.ConfigHome(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "cc-haha"), nil
}
func (resolver *ConfigResolver) TeamsDir(scope *cooperative.Scope) (string, error) {
	home, err := resolver.ConfigHome(scope)
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "teams"), nil
}
func (resolver *ConfigResolver) ClearCache(scope *cooperative.Scope) error {
	if resolver == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, resolver.owner); err != nil {
		return err
	}
	clear(resolver.cache)
	return nil
}

// These methods preserve the public lodash memoize cache controls. A primed
// value is returned literally; it bypasses fallback and NFC computation.
func (resolver *ConfigResolver) CacheLookup(scope *cooperative.Scope) (string, bool, error) {
	if resolver == nil {
		return "", false, cooperative.ErrRestricted
	}
	if err := check(scope, resolver.owner); err != nil {
		return "", false, err
	}
	key, present := resolver.host.Environment("CLAUDE_CONFIG_DIR")
	value, found := resolver.cache[configKey{key, present}]
	return value, found, nil
}
func (resolver *ConfigResolver) PrimeCache(scope *cooperative.Scope, value string) error {
	if resolver == nil {
		return cooperative.ErrRestricted
	}
	if err := check(scope, resolver.owner); err != nil {
		return err
	}
	if err := valid(value); err != nil {
		return err
	}
	key, present := resolver.host.Environment("CLAUDE_CONFIG_DIR")
	resolver.cache[configKey{key, present}] = value
	return nil
}
func (resolver *ConfigResolver) DeleteCache(scope *cooperative.Scope) (bool, error) {
	if resolver == nil {
		return false, cooperative.ErrRestricted
	}
	if err := check(scope, resolver.owner); err != nil {
		return false, err
	}
	key, present := resolver.host.Environment("CLAUDE_CONFIG_DIR")
	entry := configKey{key, present}
	_, found := resolver.cache[entry]
	delete(resolver.cache, entry)
	return found, nil
}
func (resolver *ConfigResolver) CacheSize(scope *cooperative.Scope) (int, error) {
	if resolver == nil {
		return 0, cooperative.ErrRestricted
	}
	if err := check(scope, resolver.owner); err != nil {
		return 0, err
	}
	return len(resolver.cache), nil
}
