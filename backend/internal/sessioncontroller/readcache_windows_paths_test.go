package sessioncontroller

import (
	"testing"
)

func TestWindowsResumeReadCachePathConversionKeepsDriveAndUNCIdentity(t *testing.T){
	for _,row:=range []struct{value,want string}{
		{`"/c/Work/file.txt"`,`C:\Work\file.txt`},
		{`"c:/Work/file.txt"`,`c:\Work\file.txt`},
		{`"/d/Work/file.txt"`,`D:\Work\file.txt`},
		{`"../file.txt"`,`C:\file.txt`},
		{`"\\\\server\\share\\file.txt"`,`\\server\share\file.txt`},
		{`"~/file.txt"`,`C:\User\file.txt`},
	}{got,err:=expandFilePathPlatform([]byte(row.value),`C:\Work`,`C:\User`,"windows");if err!=nil || got!=row.want{t.Fatalf("%s Go=%q Source=%q %v",row.value,got,row.want,err)}}
	for _,value:=range []string{`"bad\u0000path"`,`"\ud800"`,`null`,`false`}{if _,err:=expandFilePathPlatform([]byte(value),`C:\Work`,`C:\User`,"windows");err==nil{t.Fatal("invalid old path admitted",value)}}
}
