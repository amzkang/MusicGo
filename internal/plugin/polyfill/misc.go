package polyfill

import (
	"fmt"
	"log"
	"math/big"
	"net/url"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
)

// RegisterMisc registers console, env, process, qs, dayjs, big-integer
func RegisterMisc(vm *goja.Runtime, userVars map[string]string) {
	// Plugin output may contain credentials or response data. Enable it only
	// explicitly, and bound each runtime's output to avoid flooding the log.
	console := vm.NewObject()
	enabled := os.Getenv("MUSICGO_PLUGIN_DEBUG") == "1"
	remaining := 100
	_ = console.Set("log", func(call goja.FunctionCall) goja.Value {
		if !enabled || remaining == 0 {
			return goja.Undefined()
		}
		remaining--
		var parts []string
		for i, arg := range call.Arguments {
			if i == 16 {
				parts = append(parts, "...")
				break
			}
			text := arg.String()
			if len(text) > 512 {
				text = text[:512] + "..."
			}
			parts = append(parts, text)
		}
		log.Printf("[Plugin JS] %s", strings.Join(parts, " "))
		return goja.Undefined()
	})
	_ = console.Set("warn", console.Get("log"))
	_ = console.Set("error", console.Get("log"))
	_ = console.Set("info", console.Get("log"))
	_ = console.Set("debug", console.Get("log"))
	_ = vm.Set("console", console)

	// 2. env
	env := vm.NewObject()
	_ = env.Set("getUserVariables", func() map[string]string {
		if userVars == nil {
			return map[string]string{}
		}
		return userVars
	})
	_ = env.Set("os", runtime.GOOS)
	_ = env.Set("appVersion", "0.1.0")
	_ = env.Set("lang", "zh-CN")
	_ = vm.Set("env", env)

	// 3. process
	process := vm.NewObject()
	_ = process.Set("platform", runtime.GOOS)
	_ = process.Set("version", "v20.0.0")
	_ = process.Set("env", vm.NewObject())
	_ = vm.Set("process", process)
}

// CreateQSModule creates the 'qs' module
func CreateQSModule(vm *goja.Runtime) *goja.Object {
	qs := vm.NewObject()
	_ = qs.Set("stringify", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 || goja.IsNull(call.Arguments[0]) || goja.IsUndefined(call.Arguments[0]) {
			return vm.ToValue("")
		}
		obj := call.Arguments[0].Export()
		m, ok := obj.(map[string]interface{})
		if !ok {
			return vm.ToValue("")
		}
		v := url.Values{}
		for k, val := range m {
			if val == nil {
				continue
			}
			v.Set(k, fmt.Sprint(val))
		}
		return vm.ToValue(v.Encode())
	})
	_ = qs.Set("parse", func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return vm.NewObject()
		}
		raw := call.Arguments[0].String()
		values, err := url.ParseQuery(raw)
		if err != nil {
			return vm.NewObject()
		}
		res := vm.NewObject()
		for k, vals := range values {
			if len(vals) == 1 {
				_ = res.Set(k, vals[0])
			} else {
				_ = res.Set(k, vals)
			}
		}
		return res
	})
	return qs
}

// CreateDayjsModule creates the 'dayjs' module
func CreateDayjsModule(vm *goja.Runtime) goja.Value {
	dayjsFn := func(call goja.FunctionCall) goja.Value {
		var t time.Time
		if len(call.Arguments) == 0 || goja.IsUndefined(call.Arguments[0]) {
			t = time.Now()
		} else {
			arg := call.Arguments[0].Export()
			switch v := arg.(type) {
			case int64:
				if v > 10000000000 { // milliseconds
					t = time.UnixMilli(v)
				} else {
					t = time.Unix(v, 0)
				}
			case float64:
				if v > 10000000000 {
					t = time.UnixMilli(int64(v))
				} else {
					t = time.Unix(int64(v), 0)
				}
			case string:
				parsed, err := time.Parse(time.RFC3339, v)
				if err != nil {
					parsed, _ = time.Parse("2006-01-02 15:04:05", v)
				}
				t = parsed
			default:
				t = time.Now()
			}
		}

		inst := vm.NewObject()
		_ = inst.Set("unix", func() int64 {
			return t.Unix()
		})
		_ = inst.Set("valueOf", func() int64 {
			return t.UnixMilli()
		})
		_ = inst.Set("format", func(call goja.FunctionCall) goja.Value {
			layout := "2006-01-02 15:04:05"
			if len(call.Arguments) > 0 {
				fmtStr := call.Arguments[0].String()
				// Translate standard dayjs format strings to Go layout
				fmtStr = strings.ReplaceAll(fmtStr, "YYYY", "2006")
				fmtStr = strings.ReplaceAll(fmtStr, "MM", "01")
				fmtStr = strings.ReplaceAll(fmtStr, "DD", "02")
				fmtStr = strings.ReplaceAll(fmtStr, "HH", "15")
				fmtStr = strings.ReplaceAll(fmtStr, "mm", "04")
				fmtStr = strings.ReplaceAll(fmtStr, "ss", "05")
				fmtStr = strings.ReplaceAll(fmtStr, "SSS", "000")
				layout = fmtStr
			}
			return vm.ToValue(t.Format(layout))
		})
		_ = inst.Set("year", func() int { return t.Year() })
		_ = inst.Set("month", func() int { return int(t.Month()) - 1 })
		_ = inst.Set("date", func() int { return t.Day() })
		return inst
	}

	fnVal := vm.ToValue(dayjsFn)
	obj := fnVal.ToObject(vm)
	_ = obj.Set("unix", func(sec int64) goja.Value {
		t := time.Unix(sec, 0)
		return dayjsFn(goja.FunctionCall{Arguments: []goja.Value{vm.ToValue(t.UnixMilli())}})
	})
	return fnVal
}

// CreateBigIntModule creates the 'big-integer' module
func CreateBigIntModule(vm *goja.Runtime) goja.Value {
	var bigIntFn func(call goja.FunctionCall) goja.Value
	var wrapBigInt func(val *big.Int) *goja.Object

	wrapBigInt = func(val *big.Int) *goja.Object {
		if val == nil {
			val = big.NewInt(0)
		}
		obj := vm.NewObject()
		_ = obj.Set("toString", func(call goja.FunctionCall) goja.Value {
			base := 10
			if len(call.Arguments) > 0 {
				base = int(call.Arguments[0].ToInteger())
				if base < 2 || base > 36 {
					base = 10
				}
			}
			return vm.ToValue(val.Text(base))
		})
		_ = obj.Set("modPow", func(expVal goja.Value, modVal goja.Value) goja.Value {
			exp := getBigIntFromVal(expVal)
			mod := getBigIntFromVal(modVal)
			res := new(big.Int).Exp(val, exp, mod)
			return wrapBigInt(res)
		})
		_ = obj.Set("add", func(otherVal goja.Value) goja.Value {
			res := new(big.Int).Add(val, getBigIntFromVal(otherVal))
			return wrapBigInt(res)
		})
		_ = obj.Set("subtract", func(otherVal goja.Value) goja.Value {
			res := new(big.Int).Sub(val, getBigIntFromVal(otherVal))
			return wrapBigInt(res)
		})
		_ = obj.Set("multiply", func(otherVal goja.Value) goja.Value {
			res := new(big.Int).Mul(val, getBigIntFromVal(otherVal))
			return wrapBigInt(res)
		})
		_ = obj.Set("divide", func(otherVal goja.Value) goja.Value {
			res := new(big.Int).Quo(val, getBigIntFromVal(otherVal))
			return wrapBigInt(res)
		})
		_ = obj.Set("mod", func(otherVal goja.Value) goja.Value {
			res := new(big.Int).Mod(val, getBigIntFromVal(otherVal))
			return wrapBigInt(res)
		})
		_ = obj.Set("abs", func() goja.Value {
			res := new(big.Int).Abs(val)
			return wrapBigInt(res)
		})
		_ = obj.Set("equals", func(otherVal goja.Value) bool {
			return val.Cmp(getBigIntFromVal(otherVal)) == 0
		})
		_ = obj.Set("greater", func(otherVal goja.Value) bool {
			return val.Cmp(getBigIntFromVal(otherVal)) > 0
		})
		_ = obj.Set("lesser", func(otherVal goja.Value) bool {
			return val.Cmp(getBigIntFromVal(otherVal)) < 0
		})
		return obj
	}

	bigIntFn = func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return wrapBigInt(big.NewInt(0))
		}
		raw := call.Arguments[0].Export()
		base := 10
		if len(call.Arguments) > 1 {
			base = int(call.Arguments[1].ToInteger())
		}
		bi := new(big.Int)
		switch v := raw.(type) {
		case string:
			bi.SetString(v, base)
		case int64:
			bi.SetInt64(v)
		case float64:
			bi.SetInt64(int64(v))
		case *big.Int:
			bi.Set(v)
		default:
			str := fmt.Sprint(raw)
			bi.SetString(str, base)
		}
		return wrapBigInt(bi)
	}

	return vm.ToValue(bigIntFn)
}

func getBigIntFromVal(v goja.Value) *big.Int {
	if v == nil || goja.IsNull(v) || goja.IsUndefined(v) {
		return big.NewInt(0)
	}
	if obj, ok := v.(*goja.Object); ok {
		fn := obj.Get("toString")
		if fn != nil {
			if callable, ok := goja.AssertFunction(fn); ok {
				res, err := callable(obj)
				if err == nil {
					bi := new(big.Int)
					bi.SetString(res.String(), 10)
					return bi
				}
			}
		}
	}
	exp := v.Export()
	switch val := exp.(type) {
	case int64:
		return big.NewInt(val)
	case float64:
		return big.NewInt(int64(val))
	case string:
		bi := new(big.Int)
		if strings.HasPrefix(val, "0x") || strings.HasPrefix(val, "0X") {
			bi.SetString(val[2:], 16)
		} else {
			bi.SetString(val, 10)
		}
		return bi
	}
	num, _ := strconv.ParseInt(v.String(), 10, 64)
	return big.NewInt(num)
}
