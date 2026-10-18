package sessioncontroller

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/clisettings"
	"github.com/NanmiCoder/cc-haha/backend/internal/clistartupenv"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
)

// BindNativeTaskEnvironmentOnOwner retains only the process environment already
// shared by this native Controller's startup, settings and user-context services.
// It introduces no writer, alternate settings producer or callback authority.
func (c *Controller) BindNativeTaskEnvironmentOnOwner(scope *cooperative.Scope, startup *clistartupenv.Service, settings *clisettings.Service, state *clisettings.State, environment *clistartupenv.Environment) error {
	if c == nil || scope == nil || startup == nil || settings == nil || state == nil || environment == nil || !c.options.NativeTools || !c.options.PublicPrint || c.options.PublicResumeStartup == nil || c.options.UserContext == nil {
		return cooperative.ErrRestricted
	}
	if _, err := c.todosController(scope); err != nil {
		return err
	}
	if !startup.OwnsNativeTaskEnvironment(settings, state, environment) || !c.UsesOriginalSettingsContext(startup, settings, state, environment) || c.nativeTaskEnvironment != nil && c.nativeTaskEnvironment != environment {
		return cooperative.ErrRestricted
	}
	c.nativeTaskEnvironment = environment
	return nil
}
