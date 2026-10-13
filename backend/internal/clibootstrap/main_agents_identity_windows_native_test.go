//go:build windows

package clibootstrap

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWindowsNativeAgentHardlinksAndLstatNoFollow(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "agent.md")
	if e := os.WriteFile(file, []byte("actual"), 0600); e != nil {
		t.Fatal(e)
	}
	link := filepath.Join(root, "agent-hardlink.md")
	if e := os.Link(file, link); e != nil {
		t.Fatal(e)
	}
	dev, inode, e := probeAgentsWindowsIdentity(file, true)
	if e != nil {
		t.Fatal(e)
	}
	otherDev, otherInode, e := probeAgentsWindowsIdentity(link, true)
	if e != nil || dev != otherDev || inode != otherInode || dev == 0 && inode == 0 {
		t.Fatal("native hardlink dev/inode mismatch", dev, inode, otherDev, otherInode, e)
	}
	if _, _, e = probeAgentsWindowsIdentity(filepath.Join(root, "missing"), true); !os.IsNotExist(e) {
		t.Fatal("native missing file did not fail", e)
	}
	t.Run("symlink", func(t *testing.T) {
		symlink := filepath.Join(root, "agent-symlink.md")
		if e := os.Symlink(file, symlink); e != nil {
			t.Skip("host symlink privilege unavailable", e)
		}
		linkDev, linkInode, e := probeAgentsWindowsIdentity(symlink, true)
		if e != nil {
			t.Fatal(e)
		}
		if linkDev == dev && linkInode == inode {
			t.Fatal("lstat followed symlink")
		}
		targetDev, targetInode, e := probeAgentsWindowsIdentity(symlink, false)
		if e != nil || targetDev != dev || targetInode != inode {
			t.Fatal("stat did not follow symlink", e)
		}
	})
}
