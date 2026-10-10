package insights

import "strings"

// Source initializes count maps as ordinary {}, retaining inherited function
// coercion and the legacy __proto__ setter behavior instead of Go map behavior.
var objectPrototypeFunctions = map[string]string{"constructor": "Object", "hasOwnProperty": "hasOwnProperty", "isPrototypeOf": "isPrototypeOf", "propertyIsEnumerable": "propertyIsEnumerable", "toLocaleString": "toLocaleString", "toString": "toString", "valueOf": "valueOf", "__defineGetter__": "__defineGetter__", "__defineSetter__": "__defineSetter__", "__lookupGetter__": "__lookupGetter__", "__lookupSetter__": "__lookupSetter__"}

// nodeExtension preserves Node's hidden-name and trailing slash handling.
func nodeExtension(path string) string {
	for len(path) > 0 && path[len(path)-1] == '/' {
		path = path[:len(path)-1]
	}
	leaf := path[strings.LastIndexByte(path, '/')+1:]
	dot := strings.LastIndexByte(leaf, '.')
	if dot <= 0 || leaf == ".." {
		return ""
	}
	return leaf[dot:]
}
