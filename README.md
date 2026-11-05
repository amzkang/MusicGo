# MusicGo

[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

使用 Go 和 [MyGo](https://github.com/egoist/mygo) 构建的原生 GPU 桌面音乐播放器，面向 Linux、Windows 和 macOS。界面直接由 Go 绘制，无需 Electron、Chromium、WebView 或 Node.js 运行时。

项目正在开发中，以兼容 MusicFree 插件协议和备份数据为目标。第三方插件的兼容程度、音源可用性和账号能力取决于具体插件与服务端；当前没有完整的跨平台实机验证结论。

当前以源码发布，尚未提供安装包。欢迎通过 [Issues](https://github.com/amzkang/MusicGo/issues) 反馈问题或提交改进。

## 功能

- 插件搜索与播放：使用 esbuild 和 goja 运行 CommonJS / ESM 插件，并通过 Go 实现常用宿主依赖。
- 本地收藏、歌单、播放历史、队列及会话恢复，使用纯 Go SQLite 持久化。
- 优先使用 FFmpeg 解码，缺少 FFmpeg 时回退到内置 MP3 / Vorbis 解码器；支持下载、音频缓存、均衡器与频谱。
- LRC / KRC 歌词、逐字高亮、桌面歌词，以及浅色、深色和自定义主题。
- 系统托盘、媒体控制和可选全局快捷键。

详细行为见[功能清单](docs/FEATURES.md)和[技术架构](docs/ARCHITECTURE.md)。

## 从源码构建

安装 **Go 1.27.3 或更新版本**，具体版本与依赖以 [go.mod](go.mod) 为准。推荐安装 `ffmpeg` 和 `ffprobe` 并加入 `PATH`，以支持更多音频格式和时长探测。

Linux 原生窗口需要 GTK 3，音频输出使用 PulseAudio（或 PipeWire 提供的兼容服务），不可用时回退到 ALSA；托盘图标还需要 `libayatana-appindicator3`。其他平台及系统运行库要求见 [MyGo v0.7.3 文档](https://github.com/egoist/mygo/blob/v0.7.3/docs/README.md)。

在项目根目录执行（Linux / macOS）：

```sh
git clone https://github.com/amzkang/MusicGo.git
cd MusicGo
go mod download
CGO_ENABLED=0 go build -trimpath -tags mygo_noinspector -ldflags "-s -w" -o build/musicgo ./cmd/musicgo
MUSICGO_DATA_DIR=./.data ./build/musicgo
```

Windows PowerShell：

```powershell
git clone https://github.com/amzkang/MusicGo.git
cd MusicGo
go mod download
$env:CGO_ENABLED = "0"
go build -trimpath -tags mygo_noinspector -ldflags "-s -w" -o build/musicgo.exe ./cmd/musicgo
$env:MUSICGO_DATA_DIR = ".\.data"
.\build\musicgo.exe
```

构建产物统一放在 `build/`。仓库不包含本地 Go 工具链、模块缓存或预编译程序，构建不依赖私人路径或本地 `bin/go` 包装脚本。

## 数据与插件

设置 `MUSICGO_DATA_DIR` 可指定数据目录；未设置时使用系统用户配置目录下的 `MusicGo`。配置、数据库、账号变量和缓存保存在该目录，请勿提交到仓库。

将 MusicFree 插件的 `.js` 文件放入数据目录的 `plugins/` 后启动程序，也可以通过插件页面管理。程序目前内置酷狗概念版插件，首次启动还会检查已有 MusicFree 插件目录并导入插件；内置插件源码位于 `internal/plugin/builtin/`，属于构建必需文件。插件会访问对应平台服务，请只加载可信来源的插件。

## 参与开发

先阅读[贡献说明](CONTRIBUTING.md)。本项目按当前维护约定不提交测试文件，以按改动范围选择的构建和静态检查验证；不运行 `go test`。

仓库只保留主程序源码、编译必需资源、依赖清单和维护文档。`.gitignore` 对根目录和文档采用白名单；新增顶层文件、命令或文档时需同步更新规则。临时原型、演示、截图、调研报告及个人文件保留在开发者本地。

## 致谢与许可

- [MusicFreeDesktop](https://github.com/maotoumao/MusicFreeDesktop) 与 [MusicFree](https://github.com/maotoumao/MusicFree)：项目功能与插件生态的参考。
- [MyGo](https://github.com/egoist/mygo)：原生窗口、GPU 界面与系统能力。
- [mygo-cloudmusic](https://github.com/egoist/mygo-cloudmusic)：会话恢复、缓存和播放过渡等行为设计的参考。
- [Lucide](https://lucide.dev/) 与 [Heroicons](https://heroicons.com/)：界面图标，许可与版权声明见[第三方声明](THIRD_PARTY_NOTICES.md)。
- [goja](https://github.com/dop251/goja)、[esbuild](https://github.com/evanw/esbuild)、[goquery](https://github.com/PuerkitoBio/goquery)、[modernc.org/sqlite](https://pkg.go.dev/modernc.org/sqlite)：插件执行、解析与存储能力；完整依赖见 `go.mod` 和 `go.sum`。

MusicGo 的原创代码（包括 `internal/plugin/builtin/kugou_concept.js`）按 [MIT License](LICENSE) 发布。第三方依赖与素材保留各自的许可，详见[第三方声明](THIRD_PARTY_NOTICES.md)。外部安装的插件不受本项目许可证覆盖。

MusicGo 与 MusicFree、酷狗及其他音乐服务的官方团队无隶属关系。项目许可证不授予音乐内容、第三方商标或服务接口的使用权；音乐与封面等内容的权利归相应权利人所有。
