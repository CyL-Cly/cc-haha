package whatsappcandidate

import "path/filepath"

func nodeSourceFixturePath(name string) string {
	path, err := filepath.Abs(filepath.Join("testdata", name))
	if err != nil {
		panic(err)
	}
	return path
}
