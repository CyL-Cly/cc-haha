package clibootstrap

import (
	"io/fs"
	"strconv"
)

type agentsIdentityInfo struct {
	fs.FileInfo
	identity string
}

func agentDeviceInodeIdentity(device, inode uint64, directory bool) string {
	// Source getFileIdentity skips unreliable 0:0 files, while its directory
	// cycle walker retains defined bigint dev/ino, including zero.
	if device == 0 && inode == 0 && !directory {
		return ""
	}
	return strconv.FormatUint(device, 10) + ":" + strconv.FormatUint(inode, 10)
}
func agentsInfoWithIdentity(path string, info fs.FileInfo, nofollow bool, probe func(string, bool) (uint64, uint64, error)) fs.FileInfo {
	if info == nil {
		return nil
	}
	device, inode, err := probe(path, nofollow)
	key := ""
	if err == nil {
		key = agentDeviceInodeIdentity(device, inode, info.IsDir())
	}
	return agentsIdentityInfo{FileInfo: info, identity: key}
}
