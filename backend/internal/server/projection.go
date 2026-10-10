package server

import (
	"fmt"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionstore"
	"os"
)

func projectionStat(path string) (sessionprojection.Stat, error) {
	file, err := (sessionstore.OSFileSystem{}).Open(path)
	if err != nil {
		return sessionprojection.Stat{}, err
	}
	defer file.Close()
	stat, err := file.Stat()
	if err != nil {
		return sessionprojection.Stat{}, err
	}
	native, err := os.Stat(path)
	if err != nil {
		return sessionprojection.Stat{}, err
	}
	return sessionprojection.Stat{SourceVersion: fmt.Sprintf("%s:%s:%d:%s", stat.Dev, stat.Ino, stat.Size, stat.MtimeNS), BirthtimeISO: birthtime(path, native).UTC().Format("2006-01-02T15:04:05.000Z"), MtimeISO: native.ModTime().UTC().Format("2006-01-02T15:04:05.000Z")}, nil
}
