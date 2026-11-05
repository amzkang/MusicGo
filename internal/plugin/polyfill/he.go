package polyfill

import (
	"html"

	"github.com/dop251/goja"
)

// CreateHeModule creates the 'he' module
func CreateHeModule(vm *goja.Runtime) *goja.Object {
	he := vm.NewObject()
	_ = he.Set("decode", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(html.UnescapeString(call.Arguments[0].String()))
	})
	_ = he.Set("unescape", he.Get("decode"))
	_ = he.Set("encode", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.ToValue("")
		}
		return vm.ToValue(html.EscapeString(call.Arguments[0].String()))
	})
	_ = he.Set("escape", he.Get("encode"))
	return he
}
