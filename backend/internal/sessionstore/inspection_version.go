package sessionstore

import "fmt"

// InspectionVersion additionally includes native change time. The ordinary
// paging/recovery sourceVersion deliberately keeps its established four fields.
func InspectionVersion(path string) (version string, failure error) {
	opened, err := (OSFileSystem{}).Open(path)
	if err != nil {
		return "", err
	}
	file := opened.(*osFile)
	defer func() {
		if err := file.Close(); err != nil {
			version = ""
			failure = err
		}
	}()
	info, err := file.File.Stat()
	if err != nil {
		return "", err
	}
	dev, ino, err := fileIdentity(file.File, info)
	if err != nil {
		return "", err
	}
	changed, err := fileChangeTimeNS(file.File, info)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s:%s:%d:%d:%s", dev, ino, info.Size(), info.ModTime().UnixNano(), changed), nil
}
