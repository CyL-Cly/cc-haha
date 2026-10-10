package clibootstrap

import (
	"context"
	"errors"
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sdkpermissions"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

func (m *MainPrint) persistNativePermission(scope *cooperative.Scope, binding *ownerasync.NativeBinding, update protocol.JSONObject) error {
	if !scope.BelongsTo(m.Owner) || !m.options.Native.NativeProcess || binding == nil || !binding.Owns(m.Owner) {
		return cooperative.ErrRestricted
	}
	n := m.options.Native
	log := func(e error) { _, _ = m.options.Error.Write([]byte(e.Error() + "\n")) }
	// Only the physical worker appends; its owner completion consumes the list.
	// Error writers and Harness state never run on the physical worker.
	var ignoreErrors []error
	ignore, e := clisettings.NewLocalIgnore(clisettings.LocalIgnoreOptions{Home: n.Home, Stat: n.stat, ReadFile: n.readFile, MkdirAll: func(p string) error {
		if e := n.check("mkdir", p); e != nil {
			return e
		}
		return os.MkdirAll(p, 0777)
	}, AppendFile: func(p string, data []byte) error {
		if e := n.check("appendFile", p); e != nil {
			return e
		}
		file, e := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0666)
		if e != nil {
			return e
		}
		_, e = file.Write(data)
		closeError := file.Close()
		if e == nil {
			e = closeError
		}
		return e
	}, WriteFile: func(p string, data []byte) error {
		if e := n.check("writeFile", p); e != nil {
			return e
		}
		return os.WriteFile(p, data, 0666)
	}, LogError: func(e error) { ignoreErrors = append(ignoreErrors, e) }, RunGit: func(ctx context.Context, cwd string, args []string) (int, error) {
		executable := ""
		for _, dir := range filepath.SplitList(n.Environment["PATH"]) {
			if dir == "" {
				continue
			}
			if !filepath.IsAbs(dir) {
				dir = filepath.Join(cwd, dir)
			}
			candidate := filepath.Join(dir, "git")
			if info, e := os.Stat(candidate); e == nil && !info.IsDir() && info.Mode()&0111 != 0 {
				executable = candidate
				break
			}
		}
		if executable == "" {
			return 1, errors.New("git executable absent from original runtime PATH")
		}
		// Original execFileNoThrow has a ten-minute timeout and this helper
		// does not inherit the model turn's abort signal.
		ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
		defer cancel()
		command := exec.CommandContext(ctx, executable, args...)
		command.Dir = cwd
		command.Env = nativeAgentProcessEnvironment(n)
		e := command.Run()
		if e == nil {
			return 0, nil
		}
		var exit *exec.ExitError
		if errors.As(e, &exit) {
			return exit.ExitCode(), nil
		}
		return 1, e
	}})
	if e != nil {
		return e
	}
	persistence, e := sdkpermissions.NewPersistence(sdkpermissions.PersistenceOptions{ValidatedSource: func(source string) (*protocol.JSONObject, error) {
		return m.settings.ReadPermissionValidatedSource(source)
	}, EditingSource: m.settings.ReadPermissionEditingSource, ManagedRulesOnly: func() (bool, error) {
		policy, e := m.Startup.GetSettingsForSource("policySettings")
		if e != nil {
			return false, e
		}
		return policy != nil && string(policy.Get("allowManagedPermissionRulesOnly")) == "true", nil
	}, LogError: log, UpdateSource: func(source string, patch protocol.JSONObject) error {
		result, e := m.settings.UpdatePermissionEditingSource(source, patch, clisettings.PermissionEditingBindings{StartLocalIgnore: func(cwd, filename string) error {
			ref, e := binding.Acquire(scope)
			if e != nil {
				return e
			}
			go func() {
				_ = ignore.Run(context.Background(), filename, cwd)
				receipt, e := m.Owner.SubmitIndependent(context.Background(), cooperative.Read, func(next *cooperative.Scope) error {
					for _, failure := range ignoreErrors {
						log(failure)
					}
					release, e := m.Owner.Microtask(cooperative.Read, func(*cooperative.Scope) error { ref.DeliveryFinished(); return nil })
					if e != nil {
						ref.DeliveryFinished()
					} else {
						ref.ReclaimRejectedReceipt(release)
					}
					return nil
				})
				if e == nil {
					e = receipt.Wait()
				}
				if e != nil {
					ref.DeliveryFinished()
				}
			}()
			return nil
		}})
		if e != nil {
			return e
		}
		return result.Error
	}})
	if e != nil {
		return e
	}
	return persistence.Persist(update)
}
