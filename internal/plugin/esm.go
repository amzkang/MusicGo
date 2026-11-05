package plugin

import (
	"fmt"
	"strings"
	"sync"

	"github.com/evanw/esbuild/pkg/api"
)

const transpileCacheLimit = 16 << 20

var transpileCache = struct {
	sync.Mutex
	entries map[string]string
	order   []string
	size    int
}{entries: make(map[string]string)}

// transpilePlugin lets esbuild identify module syntax, avoiding false matches in
// strings and comments. The bounded cache also covers CommonJS and VM rebuilds.
func transpilePlugin(source string) (string, error) {
	transpileCache.Lock()
	defer transpileCache.Unlock()
	if code, ok := transpileCache.entries[source]; ok {
		return code, nil
	}

	result := api.Transform(source, api.TransformOptions{
		Target: api.ES2017,
		Loader: api.LoaderJS,
		Format: api.FormatCommonJS,
	})
	code := source
	if len(result.Errors) == 0 {
		code = string(result.Code)
	} else {
		// Older CommonJS plugins can contain sloppy-mode duplicate declarations
		// that esbuild rejects. Leave these to goja's parser as before.
		for _, e := range result.Errors {
			if !strings.Contains(e.Text, "already been declared") {
				return "", fmt.Errorf("%s", e.Text)
			}
		}
	}
	entrySize := len(source) + len(code)
	if entrySize <= transpileCacheLimit {
		for transpileCache.size+entrySize > transpileCacheLimit {
			old := transpileCache.order[0]
			transpileCache.order = transpileCache.order[1:]
			transpileCache.size -= len(old) + len(transpileCache.entries[old])
			delete(transpileCache.entries, old)
		}
		transpileCache.entries[source] = code
		transpileCache.order = append(transpileCache.order, source)
		transpileCache.size += entrySize
	}
	return code, nil
}
