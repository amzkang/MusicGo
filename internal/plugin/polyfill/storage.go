package polyfill

import "github.com/dop251/goja"

// Storage is the per-plugin persistent key-value store the host injects.
// MusicFree plugins keep cookies, tokens and preferences in
// window.localStorage; the host scopes every entry by plugin platform.
type Storage interface {
	Get(key string) (string, bool)
	Set(key, value string)
	Delete(key string)
	Keys() []string
	Clear()
}

// RegisterStorage injects window.localStorage backed by the host's per-plugin
// storage. A nil storage leaves the environment untouched, so discovery loads
// and hosts without a store keep working.
func RegisterStorage(vm *goja.Runtime, storage Storage) {
	if storage == nil {
		return
	}
	ls := vm.NewObject()
	_ = ls.Set("getItem", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		if v, ok := storage.Get(call.Arguments[0].String()); ok {
			return vm.ToValue(v)
		}
		return goja.Null()
	})
	_ = ls.Set("setItem", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) < 2 {
			panic(vm.NewTypeError("setItem expects (key, value)"))
		}
		storage.Set(call.Arguments[0].String(), call.Arguments[1].String())
		return goja.Undefined()
	})
	_ = ls.Set("removeItem", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		storage.Delete(call.Arguments[0].String())
		return goja.Undefined()
	})
	_ = ls.Set("key", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Null()
		}
		index := int(call.Arguments[0].ToInteger())
		keys := storage.Keys()
		if index < 0 || index >= len(keys) {
			return goja.Null()
		}
		return vm.ToValue(keys[index])
	})
	_ = ls.Set("clear", func(call goja.FunctionCall) goja.Value {
		storage.Clear()
		return goja.Undefined()
	})
	_ = vm.Set("localStorage", ls)

	// Plugins reach the store through window.localStorage; some also use the
	// bare global. Expose both views of the same object.
	if w := vm.Get("window"); w != nil && !goja.IsUndefined(w) && !goja.IsNull(w) {
		if wo, ok := w.(*goja.Object); ok {
			_ = wo.Set("localStorage", ls)
			return
		}
	}
	window := vm.NewObject()
	_ = window.Set("localStorage", ls)
	_ = vm.Set("window", window)
}
