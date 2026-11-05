package polyfill

import (
	"strconv"
	"strings"

	"github.com/PuerkitoBio/goquery"
	"github.com/dop251/goja"
)

// CreateCheerioModule creates the 'cheerio' module
func CreateCheerioModule(vm *goja.Runtime) *goja.Object {
	cheerio := vm.NewObject()

	loadFn := func(call goja.FunctionCall) goja.Value {
		if len(call.Arguments) == 0 {
			return goja.Undefined()
		}
		htmlStr := call.Arguments[0].String()
		doc, err := goquery.NewDocumentFromReader(strings.NewReader(htmlStr))
		if err != nil {
			return goja.Undefined()
		}

		var wrapSelection func(s *goquery.Selection) *goja.Object

		// The selections that were handed to the plugin, so that "$(element)"
		// can find the element again.
		selections := map[*goja.Object]*goquery.Selection{}

		wrapSelection = func(s *goquery.Selection) *goja.Object {
			obj := vm.NewObject()
			_ = obj.Set("length", s.Length())
			_ = obj.Set("size", func() int { return s.Length() })

			// The name of the tag, as the DOM node of cheerio gives it: in
			// lower case, as the plugins compare it with.
			if s.Length() > 0 {
				_ = obj.Set("tagName", strings.ToLower(s.Get(0).Data))
			} else {
				_ = obj.Set("tagName", "")
			}

			selections[obj] = s

			_ = obj.Set("text", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) > 0 {
					s.SetText(call.Arguments[0].String())
					return goja.Undefined()
				}
				return vm.ToValue(s.Text())
			})

			_ = obj.Set("html", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) > 0 {
					s.SetHtml(call.Arguments[0].String())
					return goja.Undefined()
				}
				h, _ := s.Html()
				return vm.ToValue(h)
			})

			_ = obj.Set("attr", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return goja.Undefined()
				}
				attrName := call.Arguments[0].String()
				if len(call.Arguments) > 1 {
					s.SetAttr(attrName, call.Arguments[1].String())
					return goja.Undefined()
				}
				val, exists := s.Attr(attrName)
				if !exists {
					return goja.Undefined()
				}
				return vm.ToValue(val)
			})

			_ = obj.Set("removeAttr", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return goja.Undefined()
				}
				s.RemoveAttr(call.Arguments[0].String())
				return goja.Undefined()
			})

			_ = obj.Set("addClass", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return goja.Undefined()
				}
				s.AddClass(call.Arguments[0].String())
				return goja.Undefined()
			})

			_ = obj.Set("removeClass", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return goja.Undefined()
				}
				s.RemoveClass(call.Arguments[0].String())
				return goja.Undefined()
			})

			_ = obj.Set("hasClass", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return vm.ToValue(false)
				}
				return vm.ToValue(s.HasClass(call.Arguments[0].String()))
			})

			_ = obj.Set("data", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return goja.Undefined()
				}
				name := call.Arguments[0].String()
				if len(call.Arguments) > 1 {
					s.SetAttr("data-"+name, call.Arguments[1].String())
					return goja.Undefined()
				}
				val, exists := s.Attr("data-" + name)
				if !exists {
					return goja.Undefined()
				}
				return vm.ToValue(val)
			})

			_ = obj.Set("is", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return vm.ToValue(false)
				}
				return vm.ToValue(s.Is(call.Arguments[0].String()))
			})

			_ = obj.Set("index", func() int {
				return s.Index()
			})

			_ = obj.Set("get", func(idx int) goja.Value {
				if idx < 0 || idx >= s.Length() {
					return goja.Undefined()
				}
				return wrapSelection(s.Eq(idx))
			})

			_ = obj.Set("slice", func(call goja.FunctionCall) goja.Value {
				start, end := 0, s.Length()
				if len(call.Arguments) > 0 {
					start = int(call.Arguments[0].ToInteger())
				}
				if len(call.Arguments) > 1 {
					end = int(call.Arguments[1].ToInteger())
				}
				return wrapSelection(s.Slice(start, end))
			})

			_ = obj.Set("next", func() goja.Value {
				return wrapSelection(s.Next())
			})

			_ = obj.Set("prev", func() goja.Value {
				return wrapSelection(s.Prev())
			})

			_ = obj.Set("siblings", func() goja.Value {
				return wrapSelection(s.Siblings())
			})

			_ = obj.Set("contents", func() goja.Value {
				return wrapSelection(s.Contents())
			})

			_ = obj.Set("clone", func() goja.Value {
				return wrapSelection(s.Clone())
			})

			_ = obj.Set("prop", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return goja.Undefined()
				}
				propName := call.Arguments[0].String()
				if strings.ToLower(propName) == "tagname" {
					if s.Length() > 0 {
						node := s.Get(0)
						return vm.ToValue(strings.ToUpper(node.Data))
					}
					return goja.Undefined()
				}
				val, exists := s.Attr(propName)
				if exists {
					return vm.ToValue(val)
				}
				return goja.Undefined()
			})

			_ = obj.Set("find", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return wrapSelection(s.Find(""))
				}
				sel := call.Arguments[0].String()
				return wrapSelection(s.Find(sel))
			})

			_ = obj.Set("children", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return wrapSelection(s.Children())
				}
				return wrapSelection(s.ChildrenFiltered(call.Arguments[0].String()))
			})

			_ = obj.Set("parent", func() goja.Value {
				return wrapSelection(s.Parent())
			})

			_ = obj.Set("parents", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return wrapSelection(s.Parents())
				}
				return wrapSelection(s.ParentsFiltered(call.Arguments[0].String()))
			})

			_ = obj.Set("first", func() goja.Value {
				return wrapSelection(s.First())
			})

			_ = obj.Set("last", func() goja.Value {
				return wrapSelection(s.Last())
			})

			_ = obj.Set("eq", func(idx int) goja.Value {
				return wrapSelection(s.Eq(idx))
			})

			_ = obj.Set("filter", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return wrapSelection(s)
				}
				arg := call.Arguments[0]
				if fn, ok := goja.AssertFunction(arg); ok {
					var nodes []*goquery.Selection
					s.Each(func(i int, single *goquery.Selection) {
						// Cheerio gives the index first, and the element
						// second, as the plugins expect.
						v, err := fn(nil, vm.ToValue(i), wrapSelection(single))
						if err == nil && v.ToBoolean() {
							nodes = append(nodes, single)
						}
					})
					if len(nodes) == 0 {
						return wrapSelection(doc.Find("__empty__"))
					}
					combined := nodes[0]
					for i := 1; i < len(nodes); i++ {
						combined = combined.AddSelection(nodes[i])
					}
					return wrapSelection(combined)
				}
				return wrapSelection(s.Filter(arg.String()))
			})

			_ = obj.Set("each", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return obj
				}
				fn, ok := goja.AssertFunction(call.Arguments[0])
				if !ok {
					return obj
				}
				s.Each(func(i int, single *goquery.Selection) {
					_, _ = fn(nil, vm.ToValue(i), wrapSelection(single))
				})
				return obj
			})

			_ = obj.Set("map", func(call goja.FunctionCall) goja.Value {
				if len(call.Arguments) == 0 {
					return obj
				}
				fn, ok := goja.AssertFunction(call.Arguments[0])
				if !ok {
					return obj
				}
				// The result is an array, as cheerio gives one, so that the
				// plugin may hand it back and it stays a list of items.
				var items []goja.Value
				s.Each(func(i int, single *goquery.Selection) {
					res, err := fn(nil, vm.ToValue(i), wrapSelection(single))
					if err == nil && !goja.IsNull(res) && !goja.IsUndefined(res) {
						items = append(items, res)
					}
				})
				results := vm.NewArray()
				for i, item := range items {
					_ = results.Set(strconv.Itoa(i), item)
				}
				_ = results.Set("get", func() interface{} {
					return results
				})
				_ = results.Set("toArray", func() interface{} {
					return results
				})
				return results
			})

			_ = obj.Set("toArray", func() []interface{} {
				var items []interface{}
				s.Each(func(i int, single *goquery.Selection) {
					items = append(items, wrapSelection(single))
				})
				return items
			})

			// A selection is walked with "for...of" by the plugins that read
			// the children of an element, so it is made iterable.
			iterFn := vm.ToValue(func(call goja.FunctionCall) goja.Value {
				iter := vm.NewObject()
				items := make([]goja.Value, 0, s.Length())
				s.Each(func(i int, single *goquery.Selection) {
					items = append(items, wrapSelection(single))
				})
				index := 0
				_ = iter.Set("next", func() goja.Value {
					res := vm.NewObject()
					if index < len(items) {
						_ = res.Set("value", items[index])
						_ = res.Set("done", false)
						index++
					} else {
						_ = res.Set("value", goja.Undefined())
						_ = res.Set("done", true)
					}
					return res
				})
				return iter
			})
			_ = obj.Set("@@iterator", iterFn)
			_ = obj.SetSymbol(goja.SymIterator, iterFn)

			return obj
		}

		// $ is a function that takes selector
		dollarFn := func(call goja.FunctionCall) goja.Value {
			if len(call.Arguments) == 0 {
				return wrapSelection(doc.Selection)
			}
			arg := call.Arguments[0]
			// "$" also takes an element the plugin has already found, and
			// gives a selection of it back.
			if argObj, ok := arg.(*goja.Object); ok {
				if s, known := selections[argObj]; known {
					return wrapSelection(s)
				}
			}
			sel := arg.String()
			return wrapSelection(doc.Find(sel))
		}

		dollarVal := vm.ToValue(dollarFn)
		dollarObj := dollarVal.ToObject(vm)
		_ = dollarObj.Set("root", func() goja.Value {
			return wrapSelection(doc.Selection)
		})
		_ = dollarObj.Set("html", func() string {
			h, _ := doc.Html()
			return h
		})
		return dollarVal
	}

	_ = cheerio.Set("load", loadFn)
	_ = cheerio.Set("default", cheerio)
	return cheerio
}
