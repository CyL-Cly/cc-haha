package imagegeneration

// UserImages is the original insertion-ordered Set used only for paths the
// human explicitly attached with @. Glob/Read do not register images.
// The owning session serializes mutations; no second tool/session store exists.
type UserImages struct{ paths []string }

func (r *UserImages) RegisterResolved(path string) {
	for i, old := range r.paths {
		if old == path {
			r.paths = append(r.paths[:i], r.paths[i+1:]...)
			break
		}
	}
	for len(r.paths) >= 200 {
		r.paths = r.paths[1:]
	}
	r.paths = append(r.paths, path)
}
func (r *UserImages) ContainsResolved(path string) bool {
	if r == nil {
		return false
	}
	for _, old := range r.paths {
		if old == path {
			return true
		}
	}
	return false
}
func (r *UserImages) Clear() { r.paths = nil }
