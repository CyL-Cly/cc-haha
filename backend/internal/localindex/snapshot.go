package localindex

import "os"

// SnapshotPath returns the same native identity/mtime/ctime primitive used by
// fingerprints, including Windows ChangeTime rather than creation time.
func SnapshotPath(path string) (FileSnapshot, error) {
	file, e := os.Open(path)
	if e != nil {
		return FileSnapshot{}, e
	}
	defer file.Close()
	return fileSnapshot(file)
}
func SnapshotInfo(info os.FileInfo) (FileSnapshot, error) { return snapshot(info) }
