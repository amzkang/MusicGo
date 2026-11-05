package polyfill

import (
	"context"
	"fmt"
	"strings"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
)

// SetupEnvironment sets up the runtime environment and modules for a MusicFree plugin
func SetupEnvironment(vm *goja.Runtime, loop *eventloop.EventLoop, userVars map[string]string, storage Storage, contexts ...context.Context) func(name string) goja.Value {
	return SetupEnvironmentWithBoundary(vm, loop, userVars, storage, nil, contexts...)
}

// SetupEnvironmentWithBoundary also guards asynchronous host jobs on the VM loop.
func SetupEnvironmentWithBoundary(vm *goja.Runtime, loop *eventloop.EventLoop, userVars map[string]string, storage Storage, boundary func(func()), contexts ...context.Context) func(name string) goja.Value {
	RegisterMisc(vm, userVars)
	RegisterStorage(vm, storage)

	// Pre-create modules
	axiosModule := createAxiosModule(vm, loop, boundary, contexts...)
	cheerioModule := CreateCheerioModule(vm)
	cryptoModule := CreateCryptoJSModule(vm)
	heModule := CreateHeModule(vm)
	qsModule := CreateQSModule(vm)
	dayjsModule := CreateDayjsModule(vm)
	bigIntModule := CreateBigIntModule(vm)
	lyricModule := createLyricModule(vm)

	requireFn := func(name string) goja.Value {
		cleaned := strings.ToLower(strings.TrimSpace(name))
		switch cleaned {
		case "axios":
			return axiosModule
		case "cheerio":
			return cheerioModule
		case "crypto-js", "cryptojs":
			return cryptoModule
		case "he":
			return heModule
		case "qs":
			return qsModule
		case "dayjs":
			return dayjsModule
		case "big-integer", "biginteger", "bigint":
			return bigIntModule
		case "musicgo/lyric":
			return lyricModule
		default:
			panic(vm.NewGoError(fmt.Errorf("cannot find module %q", name)))
		}
	}

	reqVal := vm.ToValue(func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			panic(vm.NewTypeError("require expects a module name"))
		}
		return requireFn(call.Arguments[0].String())
	})

	_ = vm.Set("require", reqVal)
	_ = vm.Set("__musicfree_require", reqVal)

	return requireFn
}
