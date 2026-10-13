//go:build windows

package sessioncontroller

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/NanmiCoder/cc-haha/backend/internal/builtinwrite"
	"github.com/NanmiCoder/cc-haha/backend/internal/cooperative"
	"github.com/NanmiCoder/cc-haha/backend/internal/nativepaths"
	"github.com/NanmiCoder/cc-haha/backend/internal/ownerasync"
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/NanmiCoder/cc-haha/backend/internal/sessionprojection"
)

// This is a real Windows-kernel regression, never relabeled as a macOS run.
// The existing entry guards remain until all Windows producers are integrated.
func TestActualWindowsOldPOSIXReadHistoryRestoresNativeCacheAndWrite(t *testing.T){
	home:=t.TempDir();target:=filepath.Join(home,"恢复.txt")
	if err:=os.WriteFile(target,[]byte("before"),0600);err!=nil{t.Fatal(err)}
	history:=toolMessages("Read",raw(map[string]string{"file_path":nativepaths.WindowsToPOSIX(target)}),quote("1→before"),nil)
	history[1].Set("timestamp",quote(time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)))
	cache,err:=ExtractReadCache(history,home,home,OSFiles{},sessionprojection.ParseDateUTC);if err!=nil{t.Fatal(err)}
	state,exists:=cache.Get(target);if !exists || text(state.Content)!="before"{t.Fatal("legacy /c cache missing",cache.Entries())}
	owner:=cooperative.New();defer owner.Close();refs:=&ownerasync.NativeReferences{};binding,err:=refs.Bind(owner);if err!=nil{t.Fatal(err)}
	actual,err:=builtinwrite.NewFileStateCache(owner,100,25*1024*1024);if err!=nil{t.Fatal(err)}
	permission:=protocol.NewJSONObject(protocol.JSONField{Name:"mode",Value:quote("bypassPermissions")},protocol.JSONField{Name:"alwaysDenyRules",Value:raw(map[string][]string{})})
	writer,err:=builtinwrite.New(builtinwrite.Options{Owner:owner,NativeBinding:binding,Cache:actual,Home:home,CWD:home,PID:os.Getpid(),NowMillis:func()float64{return float64(time.Now().UnixMilli())},Environment:func(string)string{return ""},PermissionContext:func(*cooperative.Scope)(protocol.JSONObject,error){return permission.Clone(),nil},Host:builtinwrite.Host{Platform:"windows"},HostState:func(*cooperative.Scope)(builtinwrite.Host,error){return builtinwrite.Host{Platform:"windows",NonInteractive:true},nil}})
	if err!=nil{t.Fatal("actual Windows writer startup",err)}
	if err:=owner.Run(context.Background(),func(scope *cooperative.Scope)error{
		if err:=actual.Set(scope,target,builtinwrite.FileState{Content:state.Content,Timestamp:state.Timestamp});err!=nil{return err}
		in:=protocol.NewJSONObject(protocol.JSONField{Name:"file_path",Value:quote(nativepaths.WindowsToPOSIX(target))},protocol.JSONField{Name:"content",Value:quote("WINDOWS_RESUME_NATIVE_WRITE_OK")})
		if err:=writer.Backfill(&in);err!=nil{return err};if err:=writer.Validate(context.Background(),in,scope);err!=nil{return err}
		decision,err:=writer.Permission(scope,in);if err!=nil{return err};if decision!="allow"{return fmt.Errorf("restored actual writer decision %s",decision)}
		_,err=writer.Call(context.Background(),in,scope);return err
	});err!=nil{t.Fatal(err)}
	data,err:=os.ReadFile(target);if err!=nil || string(data)!="WINDOWS_RESUME_NATIVE_WRITE_OK"{t.Fatalf("actual Windows replacement %q %v",data,err)}
}
