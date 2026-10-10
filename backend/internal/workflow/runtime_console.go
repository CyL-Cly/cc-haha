package workflow

import (
	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

func (x *execution) initializeConsole() error {
	if e := x.vm.Set("__workflow_console_write", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).(goja.String)
		units := make([]uint16, text.Length())
		for i := range units {
			units[i] = text.CharAt(i)
		}
		if e := x.emit(protocol.NewJSONObject(protocol.JSONField{Name: "type", Value: quoted("workflow_log")}, protocol.JSONField{Name: "message", Value: protocol.QuoteJavaScriptUTF16(units)})); e != nil {
			panic(x.errorValue(e))
		}
		return goja.Undefined()
	}); e != nil {
		return e
	}
	value, e := x.vm.RunScript("workflow-console.js", `(()=>{const write=__workflow_console_write;delete globalThis.__workflow_console_write;const render=args=>args.map(a=>{if(typeof a==='string')return a;try{return JSON.stringify(a)??String(a)}catch{return '[object]'}}).join(' ');const emit=(...args)=>{write(render(args))};return Object.freeze({__proto__:null,log:emit,info:emit,warn:emit,error:emit,debug:emit})})()`)
	if e != nil {
		return e
	}
	return x.vm.Set("console", value)
}
