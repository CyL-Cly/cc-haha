package computeruse

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func runToolSource(t *testing.T, input any, script string) []byte {
	t.Helper()
	bun := os.Getenv("CC_HAHA_PINNED_BUN")
	if bun == "" {
		t.Skip("actual Source requires pinned Bun 1.3.14")
	}
	options := fixtureOptions(t)
	root, _ := filepath.Abs("../../..")
	file := filepath.Join(options.HomeDir, "tools-source.ts")
	if err := os.WriteFile(file, []byte(script), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(bun, file, root)
	command.Dir = options.WorkDir
	for key, value := range options.Environment {
		command.Env = append(command.Env, key+"="+value)
	}
	command.Env = append(command.Env, "NODE_ENV=test")
	command.Stdin = strings.NewReader(string(encode(input)))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("Source=%s err=%v", output, err)
	}
	return output
}

type toolCase struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args"`
	Mode string          `json:"mode,omitempty"`
}

func TestActualSourceNativeStaticArgumentsAndSystemKeyAliases(t *testing.T) {
	cases := []toolCase{{"list_apps", json.RawMessage(`[]`), ""}, {"unknown", json.RawMessage(`{}`), ""}, {"get_app_state", json.RawMessage(`{}`), ""}, {"get_app_state", json.RawMessage(`{"app":" X ","disableDiff":null}`), ""}, {"get_app_state", json.RawMessage(`{"app":"X","disableDiff":false,"disable_diff":null}`), ""}, {"click", json.RawMessage(`{"app":"X","element_index":17}`), ""}, {"click", json.RawMessage(`{"app":"X","element_index":"g01:4"}`), ""}, {"click", json.RawMessage(`{"app":"X","x":1}`), ""}, {"click", json.RawMessage(`{"app":"X","x":-1,"y":2}`), ""}, {"click", json.RawMessage(`{"app":"X","x":1,"y":2,"click_count":1.5}`), ""}, {"click", json.RawMessage(`{"app":"X","x":1,"y":2,"mouse_button":6}`), ""}, {"click", json.RawMessage(`{"app":"X","element_index":"g0:0","mouse_button":"R"}`), ""}, {"set_value", json.RawMessage(`{"app":"X","element_index":"g17:4","value":""}`), ""}, {"set_value", json.RawMessage(`{"app":"X","element_index":"g17:4","value":false}`), ""}, {"select_text", json.RawMessage(`{"app":"X","element_index":"g17:4","text":"x","selection":null,"selection_type":"text"}`), ""}, {"select_text", json.RawMessage(`{"app":"X","element_index":"g17:4","text":"x","selection":{}}`), ""}, {"scroll", json.RawMessage(`{"app":"X","x":1,"y":2,"direction":"D","amount":0.5}`), ""}, {"scroll", json.RawMessage(`{"app":"X","element_index":"g17:4","direction":"down","pages":null,"amount":1}`), ""}, {"drag", json.RawMessage(`{"app":"X","from":null,"from_x":1,"from_y":2,"to_x":3,"to_y":4}`), ""}, {"drag", json.RawMessage(`{"app":"X","from":{"x":1,"y":2},"to":{"x":3,"y":4}}`), ""}, {"press_key", json.RawMessage(`{"app":"X","key":"super+a cmd+opt+esc"}`), ""}, {"type_text", json.RawMessage(`{"app":"X","text":""}`), ""}, {"paste", json.RawMessage(`{"app":"X","text":"text","format":"md"}`), ""}, {"paste", json.RawMessage(`{"app":"X","text":"text","format":"png"}`), ""}, {"sequence", json.RawMessage(`{"extra":true}`), ""}, {"sequence", json.RawMessage(`{"app":"X","steps":[{"tool":"scroll","element_index":"g17:4","direction":"down","amount":1}]}`), ""}, {"sequence", json.RawMessage(`{"app":"X","steps":[{"tool":"click","x":1,"y":2},{"tool":"type_text","text":""}]}`), ""}}
	keys := []string{"Cmd + Q", "command+q", "shift+cmd+tab", "cmd+q+a", "cmd+shift", "alt+Tab", "super+a cmd+opt+esc ctrl+v", "control_l+super_r+q", "cmd\u0085+q", "cmd\u00a0+q"}
	for alias := range modifierAliases {
		keys = append(keys, alias+"+q", alias+"+tab")
	}
	script := `const root=Bun.argv[2];const calls=await import(root+'/src/vendor/computer-use-mcp/toolCalls.ts');const key=await import(root+'/src/vendor/computer-use-mcp/keyBlocklist.ts');const input=await Bun.stdin.json();const results=input.cases.map(c=>{const r=calls.staticRequestError(c.name,c.args,{systemKeyCombos:false},'darwin');return r?{...r,nativeCallNotDispatched:true}:null});console.log(JSON.stringify({results,keys:input.keys.map(k=>[key.isSystemKeyCombo(k,'darwin'),key.isSystemKeyCombo(k,'win32')])}))`
	output := runToolSource(t, map[string]any{"cases": cases, "keys": keys}, script)
	var expected object
	if json.Unmarshal(output, &expected) != nil {
		t.Fatalf("Source=%s", output)
	}
	actual := []any{}
	for _, input := range cases {
		args, _ := decodeObject(input.Args)
		if args == nil {
			args = object{}
		}
		_, err := parseNativeTool(input.Name, args, object{"systemKeyCombos": encode(false)}, "darwin")
		if err == nil {
			actual = append(actual, nil)
			continue
		}
		bad := err.(*toolArgumentError)
		message := bad.message
		if bad.kind == "bad_args" && knownNativeTool(input.Name) {
			message = `Tool "` + input.Name + `": ` + message
		}
		actual = append(actual, toolError(message, bad.kind))
	}
	if !reflect.DeepEqual(jsonValue(t, encode(actual)), jsonValue(t, expected["results"])) {
		t.Errorf("Source static=%s\nGo=%s", expected["results"], encode(actual))
	}
	actualKeys := [][2]bool{}
	for _, key := range keys {
		actualKeys = append(actualKeys, [2]bool{IsSystemKeyCombo(key, "darwin"), IsSystemKeyCombo(key, "win32")})
	}
	if !reflect.DeepEqual(jsonValue(t, encode(actualKeys)), jsonValue(t, expected["keys"])) {
		t.Errorf("Source keys=%s\nGo=%s", expected["keys"], encode(actualKeys))
	}
}
func TestActualSourceGuardedNativeDispatchStateImagesAndErrorMetadata(t *testing.T) {
	cases := []toolCase{{"list_apps", json.RawMessage(`{}`), ""}, {"get_app_state", json.RawMessage(`{"app":"Private App","disable_diff":true}`), ""}, {"click", json.RawMessage(`{"app":"Password Manager","x":1,"y":2,"mouse_button":"r","click_count":2}`), ""}, {"set_value", json.RawMessage(`{"app":"X","element_index":"g17:4","value":""}`), ""}, {"select_text", json.RawMessage(`{"app":"X","element_index":"g17:4","text":"x","prefix":"p","selection_type":"cursor_after"}`), ""}, {"perform_secondary_action", json.RawMessage(`{"app":"X","element_index":"g17:4","action":"Raise"}`), ""}, {"scroll", json.RawMessage(`{"app":"X","element_index":"g17:4","direction":"D","amount":0.5}`), ""}, {"drag", json.RawMessage(`{"app":"X","from_x":1,"from_y":2,"to_x":3,"to_y":4,"mouse_button":1}`), ""}, {"press_key", json.RawMessage(`{"app":"X","key":"super+a"}`), ""}, {"type_text", json.RawMessage(`{"app":"X","text":"typed"}`), ""}, {"paste", json.RawMessage(`{"app":"X","text":"typed","format":"md"}`), ""}, {"click", json.RawMessage(`{"app":"X","x":1,"y":2}`), "unproven"}, {"click", json.RawMessage(`{"app":"X","x":1,"y":2}`), "stale"}, {"get_app_state", json.RawMessage(`{"app":"X"}`), "tcc"}, {"sequence", json.RawMessage(`{"app":"X","steps":[{"tool":"click","x":1,"y":2},{"tool":"type_text","text":"one"}]}`), ""}, {"sequence", json.RawMessage(`{"app":"X","steps":[{"tool":"click","x":1,"y":2}]}`), "missing-image"}}
	script := `
import {mock} from 'bun:test'
const root=Bun.argv[2];const input=await Bun.stdin.json();let current;let recorded=[]
const native=await import(root+'/src/vendor/computer-use-mcp/nativeError.ts')
const invoke=async(command,payload={})=>{
recorded.push({command,payload})
if(current.mode==='stale'&&command==='click')throw new native.NativeCommandError('native stale snapshot','stale_snapshot')
if(command==='check_permissions')return {accessibility:current.mode!=='tcc',screenRecording:null}
if(command==='resolve_app_target')return {pid:42,bundleId:'private.app',path:'/private/App.app',executablePath:'/private/App.app/Contents/MacOS/App',...(current.mode==='unproven'?{}:{launchTime:17})}
if(command==='get_app_state')return {axText:'App=private.app\n  g17:4 Button\n\n',appInstructions:'  Guide  \n',bundleId:'private.app',...(current.mode==='missing-image'?{}:{screenshot:{base64:'fixture-image',mimeType:'image/jpeg'}})}
if(command==='list_apps')return [{id:'private.app',displayName:'Private App',isRunning:true,future:17},{bundleId:'old.app',displayName:'Older App'}]
return {}
}

mock.module(root+'/src/utils/computerUse/helperBridge.ts',()=>({callHelper:invoke}))
const executor=await import(root+'/src/utils/computerUse/executor.ts')
const calls=await import(root+'/src/vendor/computer-use-mcp/toolCalls.ts')
const server=await import(root+'/src/vendor/computer-use-mcp/mcpServer.ts')
Object.defineProperty(performance,'now',{value:()=>0})
const output=[]
for(const c of input){current=c;recorded=[];calls._test.resetDeliveredAppInstructions();let held=false
const adapter={serverName:'computer-use',logger:{debug(){},silly(){},error(){}},isDisabled:()=>false,ensureOsPermissions:async()=>{const p=await invoke('check_permissions');return {granted:p.accessibility&&p.screenRecording!==false}},executor:{capabilities:{platform:'darwin'},engine:executor.createCodexEngine()}}
const ctx={getAllowedApps:()=>[],getGrantFlags:()=>({systemKeyCombos:false}),getUserDeniedBundleIds:()=>[],getSelectedDisplayId:()=>undefined,checkCuLock:async()=>({holder:held?'session':undefined,isSelf:held}),acquireCuLock:async()=>{held=true}}
const result=await server.bindSessionContext(adapter,'pixels',ctx)(c.name,c.args)
const projected={...result,...(result[calls.RESOLVED_APP_PATH]===undefined?{}:{app:result[calls.RESOLVED_APP_PATH]}),...(result[calls.APP_INVENTORY]===undefined?{}:{apps:result[calls.APP_INVENTORY]}),...(result[native.NATIVE_ERROR]===undefined?{}:{nativeError:result[native.NATIVE_ERROR]}),...(result[calls.NATIVE_CALL_NOT_DISPATCHED]===true?{nativeCallNotDispatched:true}:{})}
output.push({result:projected,calls:recorded})
}
console.log(JSON.stringify(output))
`
	output := runToolSource(t, cases, script)
	var expected []object
	if json.Unmarshal(output, &expected) != nil {
		t.Fatalf("Source=%s", output)
	}
	for index, input := range cases {
		t.Run(input.Name+"/"+input.Mode, func(t *testing.T) {
			tools, fixture := nativeToolsFixture(t, nil, "session")
			fixture.proven = input.Mode != "unproven"
			fixture.permitted = input.Mode != "tcc"
			fixture.missingImage = input.Mode == "missing-image"
			if input.Mode == "stale" {
				fixture.failCommand = "click"
			}
			result, err := tools.Call(context.Background(), input.Name, input.Args)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(jsonValue(t, encode(result)), jsonValue(t, expected[index]["result"])) {
				t.Errorf("Source result=%s\nGo=%s", expected[index]["result"], encode(result))
			}
			if !reflect.DeepEqual(jsonValue(t, encode(fixture.calls)), jsonValue(t, expected[index]["calls"])) {
				t.Errorf("Source calls=%s\nGo=%s", expected[index]["calls"], encode(fixture.calls))
			}
		})
	}
}

func TestActualSourceCLIContextFeatureWideFlagsAndLeaseMessage(t *testing.T) {
	output := runToolSource(t, nil, `const root=Bun.argv[2];const wrapper=await import(root+'/src/utils/computerUse/wrapper.tsx');const ctx=wrapper.buildSessionContext();console.log(JSON.stringify({flags:ctx.getGrantFlags(),message:ctx.formatLockHeldMessage('abcdefghijk')}))`)
	var expected object
	if json.Unmarshal(output, &expected) != nil {
		t.Fatalf("Source context=%s", output)
	}
	service := fixtureService(t, fixtureOptions(t))
	tools, err := NewNativeTools(NativeToolsOptions{Service: service, SessionID: "session"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tools.Close(context.Background()) })
	flags, err := tools.options.GrantFlags(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(jsonValue(t, flags), jsonValue(t, expected["flags"])) || tools.options.FormatLockHeld("abcdefghijk") != jsString(expected["message"]) {
		t.Fatalf("Source=%s Go flags=%s message=%s", output, flags, tools.options.FormatLockHeld("abcdefghijk"))
	}
}
