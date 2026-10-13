package application

import "github.com/NanmiCoder/cc-haha/backend/internal/server/providerconfig"

func sameNormalizedRuntime(left, right providerconfig.Selection) bool {
	return sameRuntimeProvider(left.ProviderID, right.ProviderID) && left.ModelID == right.ModelID && left.EffortLevel == right.EffortLevel && selectionFast(left) == selectionFast(right)
}

// A replayed renderer selection confirms the existing normalized override. It
// must not issue an Owner command or replace the still-running team lead.
func (d *DesktopSession) acceptReplayedRuntime(next providerconfig.RuntimeSelection) bool {
	d.mu.Lock()
	if d.sideChat {
		d.mu.Unlock()
		return false
	}
	current := d.options.RuntimeSelection
	if d.deferred != nil {
		current = &d.deferred.Selection
	}
	if current == nil || !sameNormalizedRuntime(*current, next.Selection) {
		d.mu.Unlock()
		return false
	}
	deferred := d.deferred != nil
	if deferred {
		d.deferred = &next
	} else {
		copy := next.Selection
		d.options.RuntimeSelection = &copy
	}
	d.rejectedRuntime = ""
	d.mu.Unlock()
	if !deferred {
		d.broadcastAppliedRuntime(next)
	}
	return true
}
