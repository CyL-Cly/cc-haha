package mcpapi

import (
	"fmt"
	"github.com/ebitengine/purego"
	"golang.org/x/text/collate"
	"golang.org/x/text/language"
	"runtime"
	"unicode/utf16"
	"unsafe"
)

type nameComparator struct {
	collator uintptr
	compare  func(uintptr, *uint16, int32, *uint16, int32) int32
	close    func(uintptr)
	unload   func()
	fallback *collate.Collator
}

// This is a generic Unicode collation primitive, never CLI/MCP business JS.
// ucol_strcoll's stable ABI compares UTF-16 directly; strings must not pass
// through UTF-8 replacement when they contain a lone surrogate.
// https://unicode-org.github.io/icu-docs/apidoc/released/icu4c/ucol_8h.html
func bindNameComparator(lookup func(string) (uintptr, error), unload func(), suffix string) (*nameComparator, error) {
	var open func(*byte, *int32) uintptr
	var compare func(uintptr, *uint16, int32, *uint16, int32) int32
	var close func(uintptr)
	bind := func(target any, name string) error {
		address, err := lookup(name + suffix)
		if err != nil {
			return err
		}
		purego.RegisterFunc(target, address)
		return nil
	}
	if err := bind(&open, "ucol_open"); err != nil {
		return nil, err
	}
	if err := bind(&compare, "ucol_strcoll"); err != nil {
		return nil, err
	}
	if err := bind(&close, "ucol_close"); err != nil {
		return nil, err
	}
	locale := []byte("en_US\x00")
	var status int32
	collator := open(unsafe.SliceData(locale), &status)
	runtime.KeepAlive(locale)
	if collator == 0 || status > 0 {
		return nil, fmt.Errorf("MCP API ICU collation initialization failed: %d", status)
	}
	return &nameComparator{collator: collator, compare: compare, close: close, unload: unload}, nil
}
func compatibleNameComparator() *nameComparator {
	return &nameComparator{fallback: collate.New(language.English)}
}
func (c *nameComparator) Validate(_ []uint16) error { return nil }
func (c *nameComparator) Compare(a, b []uint16) int {
	if c.fallback != nil {
		return c.fallback.CompareString(string(utf16.Decode(a)), string(utf16.Decode(b)))
	}
	result := c.compare(c.collator, unsafe.SliceData(a), int32(len(a)), unsafe.SliceData(b), int32(len(b)))
	runtime.KeepAlive(a)
	runtime.KeepAlive(b)
	return int(result)
}
func (c *nameComparator) Close() {
	if c.collator != 0 {
		c.close(c.collator)
		c.unload()
	}
}
