package bootstrapstate

import (
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

func TestConfigOriginalUndefinedEmptyAndHOMECache(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	home := t.TempDir()
	env := map[string]string{}
	homeCalls := 0
	resolver, _ := NewConfigResolver(owner, ConfigHost{Environment: func(key string) (string, bool) { value, ok := env[key]; return value, ok }, HomeDir: func() (string, error) { homeCalls++; return home, nil }})
	run(t, owner, func(scope *cooperative.Scope) error {
		first, err := resolver.ConfigHome(scope)
		if err != nil {
			return err
		}
		home = filepath.Join(home, "two")
		second, _ := resolver.ConfigHome(scope)
		if first != second || homeCalls != 1 {
			t.Error("HOME was incorrectly part of key")
		}
		env["CLAUDE_CONFIG_DIR"] = ""
		empty, _ := resolver.ConfigHome(scope)
		cc, _ := resolver.CcHahaDir(scope)
		teams, _ := resolver.TeamsDir(scope)
		if empty != "" || cc != "cc-haha" || teams != "teams" || homeCalls != 1 {
			t.Error("empty incorrectly fell back", empty, cc, teams)
		}
		delete(env, "CLAUDE_CONFIG_DIR")
		_ = resolver.ClearCache(scope)
		third, _ := resolver.ConfigHome(scope)
		if third != filepath.Join(home, ".claude") || homeCalls != 2 {
			t.Error(third, homeCalls)
		}
		return nil
	})
}
func TestConfigNFCResultOriginalKeysAndPublicCache(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	key := "config-e\u0301"
	present := true
	resolver, _ := NewConfigResolver(owner, ConfigHost{Environment: func(string) (string, bool) { return key, present }, HomeDir: func() (string, error) { t.Error("present config called HOME"); return "", nil }})
	run(t, owner, func(scope *cooperative.Scope) error {
		one, _ := resolver.ConfigHome(scope)
		key = "config-é"
		two, _ := resolver.ConfigHome(scope)
		size, _ := resolver.CacheSize(scope)
		if one != "config-é" || two != one || size != 2 {
			return fmt.Errorf("NFC applied to cache key: %q %q size=%d", one, two, size)
		}
		key = "config-e\u0301"
		_ = resolver.PrimeCache(scope, "raw-e\u0301")
		literal, _ := resolver.ConfigHome(scope)
		cached, found, _ := resolver.CacheLookup(scope)
		if literal != "raw-e\u0301" || cached != literal || !found {
			t.Error("primed value recomputed", literal, cached)
		}
		deleted, _ := resolver.DeleteCache(scope)
		deletedAgain, _ := resolver.DeleteCache(scope)
		if !deleted || deletedAgain {
			t.Error(deleted, deletedAgain)
		}
		_ = resolver.ClearCache(scope)
		size, _ = resolver.CacheSize(scope)
		if size != 0 {
			t.Error(size)
		}
		return nil
	})
}
func TestConfigFailureIsNotMemoizedOrCalledOffOwner(t *testing.T) {
	owner := cooperative.New()
	defer owner.Close()
	foreign := cooperative.New()
	defer foreign.Close()
	original := errors.New("HOME error")
	calls := 0
	resolver, _ := NewConfigResolver(owner, ConfigHost{Environment: func(string) (string, bool) { return "", false }, HomeDir: func() (string, error) {
		calls++
		if calls == 1 {
			return "", original
		}
		return "/private", nil
	}})
	run(t, foreign, func(scope *cooperative.Scope) error {
		if _, err := resolver.ConfigHome(scope); !errors.Is(err, cooperative.ErrRestricted) {
			t.Error(err)
		}
		return nil
	})
	run(t, owner, func(scope *cooperative.Scope) error {
		if _, err := resolver.ConfigHome(scope); err != original {
			t.Error(err)
		}
		if _, found, _ := resolver.CacheLookup(scope); found {
			t.Error("failed value cached")
		}
		value, err := resolver.ConfigHome(scope)
		if err != nil || value != "/private/.claude" || calls != 2 {
			t.Error(value, err, calls)
		}
		return nil
	})
}
