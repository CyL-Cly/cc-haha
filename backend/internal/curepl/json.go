package curepl

import (
	"bytes"
	"encoding/json"
	"math"
	"strconv"

	"github.com/NanmiCoder/cc-haha/backend/internal/protocol"
	"github.com/dop251/goja"
)

// Goja's default JSON decoder uses encoding/json.Token and loses unpaired
// UTF16 code units. Construct only guest primitives/objects from preserved raw
// JSON; never expose a Go map, reflected object or host error to submitted code.
func (w *worker) installJSONParser() error {
	defineValue, err := w.vm.RunString(`(object,key,value) => Object.defineProperty(object,key,{value,writable:true,enumerable:true,configurable:true})`)
	if err != nil {
		return err
	}
	define, _ := goja.AssertFunction(defineValue)
	reviveValue, err := w.vm.RunString(`(value,reviver) => {
	 if(typeof reviver !== 'function') return value;
	 const root = {'':value};
	 function walk(holder,key) {
	   let current=holder[key];
	   if(current !== null && typeof current === 'object') {
	     const keys=Array.isArray(current)?Array.from({length:current.length},(_,i)=>String(i)):Object.keys(current);
	     for(const name of keys) {
	       const next=walk(current,name);
	       if(next===undefined) delete current[name];
	       else Object.defineProperty(current,name,{value:next,writable:true,enumerable:true,configurable:true});
	     }
	   }
	   return Reflect.apply(reviver,holder,[key,current]);
	 }
	 return walk(root,'');
	}`)
	if err != nil {
		return err
	}
	revive, _ := goja.AssertFunction(reviveValue)
	var decode func(json.RawMessage) goja.Value
	decode = func(raw json.RawMessage) goja.Value {
		raw = bytes.TrimSpace(raw)
		switch raw[0] {
		case '"':
			units, _ := protocol.DecodeJavaScriptJSONString(raw)
			return goja.StringFromUTF16(units)
		case '{':
			var source protocol.JSONObject
			if err := source.UnmarshalJSON(raw); err != nil {
				panic(w.vm.NewTypeError(err.Error()))
			}
			result := w.vm.NewObject()
			for _, property := range source.OwnProperties() {
				key, _ := protocol.DecodeJavaScriptJSONString(property.NameJSON)
				if _, err := define(goja.Undefined(), result, goja.StringFromUTF16(key), decode(property.Value)); err != nil {
					panic(err)
				}
			}
			return result
		case '[':
			var rows []json.RawMessage
			_ = json.Unmarshal(raw, &rows)
			result := w.vm.NewArray()
			for index, row := range rows {
				if _, err := define(goja.Undefined(), result, w.vm.ToValue(strconv.Itoa(index)), decode(row)); err != nil {
					panic(err)
				}
			}
			return result
		case 'n':
			return goja.Null()
		case 't':
			return w.vm.ToValue(true)
		case 'f':
			return w.vm.ToValue(false)
		default:
			number, err := strconv.ParseFloat(string(raw), 64)
			if err != nil && !math.IsInf(number, 0) {
				panic(w.vm.NewTypeError(err.Error()))
			}
			return w.vm.ToValue(number)
		}
	}
	nativeParse, _ := goja.AssertFunction(w.vm.Get("JSON").ToObject(w.vm).Get("parse"))
	if err = w.vm.Get("JSON").ToObject(w.vm).Set("parse", func(call goja.FunctionCall) goja.Value {
		text := call.Argument(0).ToString()
		str, ok := text.(goja.String)
		if !ok {
			panic(w.vm.NewTypeError("JSON.parse requires a string"))
		}
		units := make([]uint16, str.Length())
		for i := range units {
			units[i] = str.CharAt(i)
		}
		source := []byte(protocol.JavaScriptJSONSourceUTF16(units))
		if !json.Valid(source) {
			_, failure := nativeParse(goja.Undefined(), text)
			if failure != nil {
				panic(failure)
			}
			panic(w.vm.NewTypeError("Invalid JSON input"))
		}
		value := decode(source)
		value, err := revive(goja.Undefined(), value, call.Argument(1))
		if err != nil {
			panic(err)
		}
		return value
	}); err != nil {
		return err
	}
	_, err = w.vm.RunString(`Object.defineProperty(JSON.parse,'name',{value:'parse'});Object.defineProperty(JSON.parse,'length',{value:2});`)
	return err
}
