package clibootstrap

// agentScopedBoundary binds an independent observation lock to the addressed
// cwd while retaining the exact primitive capabilities and OS snapshot.
func (n *NativeBoundary) agentScopedBoundary(cwd string) *NativeBoundary {
	n.internalReadState()
	n.credentialDescriptors()
	environment := n.nativeEnvironmentSnapshot()
	n.mu.Lock()
	defer n.mu.Unlock()
	absent := map[string]bool{}
	for key, value := range n.absentGit {
		absent[key] = value
	}
	return &NativeBoundary{credentials: n.credentials, CCRCredentialDirectory: n.CCRCredentialDirectory, Home: n.Home, CWD: cwd, Temp: n.Temp, ManagedRoot: n.ManagedRoot, Environment: environment, platform: n.platform, Now: n.Now, NowMillis: n.NowMillis, Timer: n.Timer, ExternalAbsent: n.ExternalAbsent, NativeProcess: n.NativeProcess, ConfigDirExplicit: n.ConfigDirExplicit, ResolvePolicy: n.ResolvePolicy, RunSecurity: n.RunSecurity, ImageLibraryPath: n.ImageLibraryPath, mdm: n.mdm, internalRead: n.internalRead, roots: append([]string{}, n.roots...), absentGit: absent}
}
