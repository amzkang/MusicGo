package polyfill

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"compress/zlib"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/dop251/goja"
	"github.com/dop251/goja_nodejs/eventloop"
	"golang.org/x/net/html/charset"
	"golang.org/x/net/publicsuffix"
)

var defaultHTTPClient = &http.Client{
	Timeout: 30 * time.Second,
}

// CreateAxiosModule creates the 'axios' module bound to an EventLoop
func CreateAxiosModule(vm *goja.Runtime, loop *eventloop.EventLoop, contexts ...context.Context) goja.Value {
	return createAxiosModule(vm, loop, nil, contexts...)
}

func createAxiosModule(vm *goja.Runtime, loop *eventloop.EventLoop, boundary func(func()), contexts ...context.Context) goja.Value {
	runOnLoop := func(fn func(*goja.Runtime)) {
		loop.RunOnLoop(func(vm *goja.Runtime) {
			if boundary != nil {
				boundary(func() { fn(vm) })
			} else {
				fn(vm)
			}
		})
	}
	ctx := context.Background()
	if len(contexts) > 0 {
		ctx = contexts[0]
	}
	var makeAxios func(defaults map[string]interface{}) goja.Value

	executeRequest := func(reqCfg map[string]interface{}, jar http.CookieJar, resolve, reject func(interface{}) error) {
		go func() {
			reqURL, _ := reqCfg["url"].(string)
			if base, ok := reqCfg["baseURL"].(string); ok && base != "" {
				if parsed, e := url.Parse(reqURL); e == nil && !parsed.IsAbs() && parsed.Host == "" {
					reqURL = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(reqURL, "/")
				}
			}
			method, _ := reqCfg["method"].(string)
			if method == "" {
				method = "GET"
			}
			method = strings.ToUpper(method)

			// Handle params
			if paramsVal, ok := reqCfg["params"]; ok && paramsVal != nil {
				parsedURL, err := url.Parse(reqURL)
				if err == nil {
					q := parsedURL.Query()
					switch p := paramsVal.(type) {
					case map[string]interface{}:
						for k, v := range p {
							if v != nil {
								q.Set(k, fmt.Sprint(v))
							}
						}
					case string:
						extraVals, _ := url.ParseQuery(p)
						for k, v := range extraVals {
							for _, subV := range v {
								q.Add(k, subV)
							}
						}
					}
					parsedURL.RawQuery = q.Encode()
					reqURL = parsedURL.String()
				}
			}

			// Handle body
			var bodyReader io.Reader
			var isJSON bool
			if dataVal, ok := reqCfg["data"]; ok && dataVal != nil {
				switch d := dataVal.(type) {
				case string:
					bodyReader = strings.NewReader(d)
				case []byte:
					bodyReader = bytes.NewReader(d)
				default:
					jsonBytes, err := json.Marshal(d)
					if err == nil {
						bodyReader = bytes.NewReader(jsonBytes)
						isJSON = true
					}
				}
			}

			httpReq, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
			if err != nil {
				runOnLoop(func(v *goja.Runtime) {
					reject(v.NewGoError(err))
				})
				return
			}

			// Default User-Agent
			httpReq.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/121.0.0.0 Safari/537.36")
			if isJSON {
				httpReq.Header.Set("Content-Type", "application/json;charset=utf-8")
			}

			// Custom headers
			if headersVal, ok := reqCfg["headers"]; ok && headersVal != nil {
				if hMap, ok := headersVal.(map[string]interface{}); ok {
					for k, v := range hMap {
						if v != nil {
							httpReq.Header.Set(k, fmt.Sprint(v))
						}
					}
				}
			}

			client := defaultHTTPClient
			if timeoutVal, ok := reqCfg["timeout"]; ok && timeoutVal != nil {
				var ms int64
				switch tv := timeoutVal.(type) {
				case int64:
					ms = tv
				case float64:
					ms = int64(tv)
				case int:
					ms = int64(tv)
				}
				if ms > 0 {
					client = &http.Client{
						Timeout: time.Duration(ms) * time.Millisecond,
					}
				}
			}
			withCredentials, _ := reqCfg["withCredentials"].(bool)
			redirectLimit, limitSet := reqCfg["maxRedirects"]
			if withCredentials || limitSet {
				copy := *client
				if withCredentials {
					copy.Jar = jar
				}
				if limitSet {
					limit, err := strconv.Atoi(fmt.Sprint(redirectLimit))
					if err == nil && limit >= 0 {
						copy.CheckRedirect = func(_ *http.Request, via []*http.Request) error {
							if len(via) > limit {
								return http.ErrUseLastResponse
							}
							return nil
						}
					}
				}
				client = &copy
			}

			resp, err := client.Do(httpReq)
			if err != nil {
				runOnLoop(func(v *goja.Runtime) {
					reject(v.NewGoError(err))
				})
				return
			}
			defer resp.Body.Close()

			var reader io.Reader = resp.Body
			// Decompress if gzip / deflate
			encoding := strings.ToLower(resp.Header.Get("Content-Encoding"))
			if strings.Contains(encoding, "gzip") {
				if gz, err := gzip.NewReader(resp.Body); err == nil {
					defer gz.Close()
					reader = gz
				}
			} else if strings.Contains(encoding, "deflate") {
				if zr, err := zlib.NewReader(resp.Body); err == nil {
					defer zr.Close()
					reader = zr
				} else {
					reader = flate.NewReader(resp.Body)
				}
			}

			var contentLimit int64
			if value, ok := reqCfg["maxContentLength"]; ok {
				contentLimit, _ = strconv.ParseInt(fmt.Sprint(value), 10, 64)
				if contentLimit > 0 && contentLimit < 1<<62 {
					reader = io.LimitReader(reader, contentLimit+1)
				}
			}
			respBytes, err := io.ReadAll(reader)
			if err != nil {
				runOnLoop(func(v *goja.Runtime) {
					reject(v.NewGoError(err))
				})
				return
			}
			if contentLimit > 0 && int64(len(respBytes)) > contentLimit {
				runOnLoop(func(v *goja.Runtime) {
					reject(v.NewGoError(fmt.Errorf("response exceeds maxContentLength")))
				})
				return
			}

			responseType, _ := reqCfg["responseType"].(string)
			if responseType != "arraybuffer" {
				encoding, _ := reqCfg["responseEncoding"].(string)
				if encoding == "" {
					if _, params, parseErr := mime.ParseMediaType(resp.Header.Get("Content-Type")); parseErr == nil {
						encoding = params["charset"]
					}
				}
				if enc, _ := charset.Lookup(encoding); enc != nil {
					if decoded, decodeErr := enc.NewDecoder().Bytes(respBytes); decodeErr == nil {
						respBytes = decoded
					}
				}
				respBytes = bytes.TrimPrefix(respBytes, []byte("\xef\xbb\xbf"))
			}

			runOnLoop(func(v *goja.Runtime) {
				resObj := v.NewObject()
				_ = resObj.Set("status", resp.StatusCode)
				_ = resObj.Set("statusText", resp.Status)

				headersObj := v.NewObject()
				for k, vals := range resp.Header {
					_ = headersObj.Set(strings.ToLower(k), strings.Join(vals, ", "))
				}
				_ = resObj.Set("headers", headersObj)
				_ = resObj.Set("config", reqCfg)

				if responseType == "arraybuffer" {
					_ = resObj.Set("data", respBytes)
				} else if responseType == "text" {
					_ = resObj.Set("data", string(respBytes))
				} else {
					// Attempt JSON parse
					var jsonVal interface{}
					if err := json.Unmarshal(respBytes, &jsonVal); err == nil {
						_ = resObj.Set("data", jsonVal)
					} else {
						_ = resObj.Set("data", string(respBytes))
					}
				}

				if resp.StatusCode >= 200 && resp.StatusCode < 400 {
					resolve(resObj)
				} else {
					// Axios treats >= 400 as error, but still attaches response
					errObj := v.NewObject()
					_ = errObj.Set("message", fmt.Sprintf("Request failed with status code %d", resp.StatusCode))
					_ = errObj.Set("response", resObj)
					_ = errObj.Set("config", reqCfg)
					reject(errObj)
				}
			})
		}()
	}

	makeAxios = func(defaults map[string]interface{}) goja.Value {
		jar, _ := cookiejar.New(&cookiejar.Options{PublicSuffixList: publicsuffix.List})
		axiosFn := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Undefined()
			}
			cfg := make(map[string]interface{})
			for k, v := range defaults {
				mergeAxiosField(cfg, k, v)
			}

			if urlStr, ok := call.Arguments[0].Export().(string); ok {
				cfg["url"] = urlStr
				if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
					if extraCfg, ok := call.Arguments[1].Export().(map[string]interface{}); ok {
						for k, v := range extraCfg {
							mergeAxiosField(cfg, k, v)
						}
					}
				}
			} else if objCfg, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
				for k, v := range objCfg {
					mergeAxiosField(cfg, k, v)
				}
			}

			promise, resolve, reject := vm.NewPromise()
			executeRequest(cfg, jar, resolve, reject)
			return vm.ToValue(promise)
		}

		fnVal := vm.ToValue(axiosFn)
		axiosObj := fnVal.ToObject(vm)

		_ = axiosObj.Set("get", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Undefined()
			}
			cfg := make(map[string]interface{})
			for k, v := range defaults {
				mergeAxiosField(cfg, k, v)
			}
			cfg["method"] = "GET"
			cfg["url"] = call.Arguments[0].String()
			if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
				if extra, ok := call.Arguments[1].Export().(map[string]interface{}); ok {
					for k, v := range extra {
						mergeAxiosField(cfg, k, v)
					}
				}
			}
			promise, resolve, reject := vm.NewPromise()
			executeRequest(cfg, jar, resolve, reject)
			return vm.ToValue(promise)
		})

		_ = axiosObj.Set("post", func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return goja.Undefined()
			}
			cfg := make(map[string]interface{})
			for k, v := range defaults {
				mergeAxiosField(cfg, k, v)
			}
			cfg["method"] = "POST"
			cfg["url"] = call.Arguments[0].String()
			if len(call.Arguments) > 1 && !goja.IsUndefined(call.Arguments[1]) {
				cfg["data"] = call.Arguments[1].Export()
			}
			if len(call.Arguments) > 2 && !goja.IsUndefined(call.Arguments[2]) {
				if extra, ok := call.Arguments[2].Export().(map[string]interface{}); ok {
					for k, v := range extra {
						mergeAxiosField(cfg, k, v)
					}
				}
			}
			promise, resolve, reject := vm.NewPromise()
			executeRequest(cfg, jar, resolve, reject)
			return vm.ToValue(promise)
		})

		_ = axiosObj.Set("request", axiosFn)
		// A jar belongs to this axios instance and is used only for requests
		// explicitly made with withCredentials. It is never persisted by the host.
		_ = axiosObj.Set("getCookieHeader", func(address string) string {
			parsed, err := url.Parse(address)
			if err != nil || (parsed.Scheme != "https" && parsed.Scheme != "http") || parsed.Host == "" {
				return ""
			}
			req := &http.Request{Header: make(http.Header)}
			for _, cookie := range jar.Cookies(parsed) {
				req.AddCookie(cookie)
			}
			return req.Header.Get("Cookie")
		})

		_ = axiosObj.Set("create", func(call goja.FunctionCall) goja.Value {
			newDefaults := make(map[string]interface{})
			for k, v := range defaults {
				mergeAxiosField(newDefaults, k, v)
			}
			if len(call.Arguments) > 0 && !goja.IsUndefined(call.Arguments[0]) {
				if custom, ok := call.Arguments[0].Export().(map[string]interface{}); ok {
					for k, v := range custom {
						mergeAxiosField(newDefaults, k, v)
					}
				}
			}
			return makeAxios(newDefaults)
		})

		_ = axiosObj.Set("default", axiosObj)
		return fnVal
	}

	return makeAxios(nil)
}

func mergeAxiosField(dst map[string]interface{}, key string, value interface{}) {
	if key == "headers" || key == "params" {
		if incoming, ok := value.(map[string]interface{}); ok {
			merged := make(map[string]interface{})
			if old, ok := dst[key].(map[string]interface{}); ok {
				for k, v := range old {
					merged[k] = v
				}
			}
			for k, v := range incoming {
				merged[k] = v
			}
			dst[key] = merged
			return
		}
	}
	dst[key] = value
}
