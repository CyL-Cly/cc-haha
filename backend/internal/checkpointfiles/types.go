// Package checkpointfiles defines the physical checkpoint values shared by
// the existing safe capture/restore implementation and the owning producer.
package checkpointfiles

import "os"

type FileState struct {
	Exists  bool
	Content []byte
	Mode    os.FileMode
}
type CapturedBackup struct {
	Name *string
	Size int64
}
