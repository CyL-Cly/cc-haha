package windowshell

// FindNativeExecutable shares the already ported native PATH/PATHEXT and file
// validity boundary with non-shell process producers such as native Git.
func FindNativeExecutable(platform, name string, environment map[string]string) string {
	return whichNative(platform, name, environment)
}
