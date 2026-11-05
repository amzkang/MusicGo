package polyfill

import (
	"github.com/dop251/goja"
	"musicgo/internal/lyric"
)

// createLyricModule exposes native decoding to the built-in lyric plugin.
func createLyricModule(vm *goja.Runtime) *goja.Object {
	module := vm.NewObject()
	_ = module.Set("decodeKrc", func(encoded string) goja.Value {
		raw, timed, err := lyric.DecodeKRC(encoded)
		if err != nil {
			panic(vm.NewGoError(err))
		}
		return vm.ToValue(map[string]string{"rawLrc": raw, "lxLrc": timed})
	})
	return module
}
