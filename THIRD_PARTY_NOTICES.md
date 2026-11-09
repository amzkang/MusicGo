# 第三方声明

MusicGo 的原创代码采用根目录的 [MIT License](LICENSE)。下列第三方内容保留原版权与许可；本项目的 MIT 许可不取代它们。

## 随源码分发的图标

- `internal/ui/icons/svg/` 中带有 `lucide-static v1.61.0` 标记的 SVG 来自 [Lucide 1.61.0](https://github.com/lucide-icons/lucide/tree/1.61.0)，采用 ISC 许可；其中衍生自 Feather 的图形同时保留 Cole Bemis 的 MIT 声明。部分文件为适配应用调整了命名或颜色属性。[上游许可](https://github.com/lucide-icons/lucide/blob/1.61.0/LICENSE)原文保留如下。
- `lock-open.svg` 与 `lock-closed.svg` 来自 [Heroicons](https://github.com/tailwindlabs/heroicons)，采用 MIT 许可。与 v2.2.0 的同名轮廓图形对应，路径空白格式与 SVG 容器属性有所调整。[上游许可](https://github.com/tailwindlabs/heroicons/blob/v2.2.0/LICENSE)原文保留如下。
- `logo.svg`、窗口控制图形和 `cmd/musicgo/` 中的应用图标由本项目提供，遵循项目 MIT 许可。

### Lucide / Feather

```text
ISC License

Copyright (c) 2026 Lucide Icons and Contributors

Permission to use, copy, modify, and/or distribute this software for any
purpose with or without fee is hereby granted, provided that the above
copyright notice and this permission notice appear in all copies.

THE SOFTWARE IS PROVIDED "AS IS" AND THE AUTHOR DISCLAIMS ALL WARRANTIES
WITH REGARD TO THIS SOFTWARE INCLUDING ALL IMPLIED WARRANTIES OF
MERCHANTABILITY AND FITNESS. IN NO EVENT SHALL THE AUTHOR BE LIABLE FOR
ANY SPECIAL, DIRECT, INDIRECT, OR CONSEQUENTIAL DAMAGES OR ANY DAMAGES
WHATSOEVER RESULTING FROM LOSS OF USE, DATA OR PROFITS, WHETHER IN AN
ACTION OF CONTRACT, NEGLIGENCE OR OTHER TORTIOUS ACTION, ARISING OUT OF
OR IN CONNECTION WITH THE USE OR PERFORMANCE OF THIS SOFTWARE.

---

The following Lucide icons are derived from the Feather project:

airplay, alert-circle, alert-octagon, alert-triangle, aperture, arrow-down-circle, arrow-down-left, arrow-down-right, arrow-down, arrow-left-circle, arrow-left, arrow-right-circle, arrow-right, arrow-up-circle, arrow-up-left, arrow-up-right, arrow-up, at-sign, calendar, cast, check, chevron-down, chevron-left, chevron-right, chevron-up, chevrons-down, chevrons-left, chevrons-right, chevrons-up, circle, clipboard, clock, code, columns, command, compass, corner-down-left, corner-down-right, corner-left-down, corner-left-up, corner-right-down, corner-right-up, corner-up-left, corner-up-right, crosshair, database, divide-circle, divide-square, dollar-sign, download, external-link, feather, frown, hash, headphones, help-circle, info, italic, key, layout, life-buoy, link-2, link, loader, lock, log-in, log-out, maximize, meh, minimize, minimize-2, minus-circle, minus-square, minus, monitor, moon, more-horizontal, more-vertical, move, music, navigation-2, navigation, octagon, pause-circle, percent, plus-circle, plus-square, plus, power, radio, rss, search, server, share, shopping-bag, sidebar, smartphone, smile, square, table-2, tablet, target, terminal, trash-2, trash, triangle, tv, type, upload, x-circle, x-octagon, x-square, x, zoom-in, zoom-out

The MIT License (MIT) (for the icons listed above)

Copyright (c) 2013-present Cole Bemis

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

### Heroicons

```text
MIT License

Copyright (c) Tailwind Labs, Inc.

Permission is hereby granted, free of charge, to any person obtaining a copy
of this software and associated documentation files (the "Software"), to deal
in the Software without restriction, including without limitation the rights
to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
copies of the Software, and to permit persons to whom the Software is
furnished to do so, subject to the following conditions:

The above copyright notice and this permission notice shall be included in all
copies or substantial portions of the Software.

THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
SOFTWARE.
```

## Go 依赖

依赖与精确版本见 [go.mod](go.mod) 和 `go.sum`，包括 MyGo、goja、goja_nodejs、esbuild、goquery、go-qrcode、golang.org/x 系列库及 modernc.org/sqlite。各依赖保留自身的许可与附加声明，可在下载后的相应版本模块源码中查看。

本仓库不分发模块缓存或 vendored 依赖。二进制发行包还需附带实际链接或内嵌的第三方许可与附加声明，包括 MyGo 内部嵌入的资源；当前首发仅提供本仓库源码。

## 插件、外部程序与内容

- `internal/plugin/builtin/` 中的酷狗概念版、QQ 音乐和汽水音乐插件是本项目内置的实现，按项目 MIT 许可提供。插件标识、接口地址和协议参数不代表获得第三方服务的授权或背书。
- 外部安装或导入的 MusicFree 插件不在本仓库分发范围内，遵循各自的许可。
- FFmpeg / ffprobe 作为可选的外部程序调用，不随本仓库分发，其许可由所安装的发行版本决定。
- 仓库不分发音乐、专辑封面、账号数据或系统字体。运行时取得的内容及第三方商标的权利归相应权利人所有。

## 行为设计参考

MusicFree / MusicFreeDesktop 的插件协议、备份格式与功能，以及 mygo-cloudmusic 的会话恢复、缓存和播放过渡行为，为本项目提供了参考。致谢不是对这些项目代码或素材的重新许可；直接引入第三方实现时，必须另外核对其原有许可并保留声明。

汽水音乐的扫码与公开目录协议参考了 [lane128/mygo-music-qishui](https://github.com/lane128/mygo-music-qishui)（AGPL-3.0-only）和 [DaviRain-Su/mygo-music-qishui](https://github.com/DaviRain-Su/mygo-music-qishui) 的公开说明；本项目未包含这两个项目的源码或运行组件。
