package localindex

import "os"

// NativeSnapshot exposes the existing source-fingerprint stat primitive to
// additional readers without introducing another ctime/identity producer.
func NativeSnapshot(info os.FileInfo) (FileSnapshot, error)  { return snapshot(info) }
func NativeFileSnapshot(file *os.File) (FileSnapshot, error) { return fileSnapshot(file) }
