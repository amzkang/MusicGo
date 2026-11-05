# 参与 MusicGo 开发

## 修改约定

- UI 使用 MyGo 原生 GPU 模式，保持 `CGO_ENABLED=0` 可构建。不要引入 WebView、HTML/CSS 界面或内嵌浏览器。
- 插件通过 esbuild、goja 和 eventloop 执行；宿主依赖通过 Go polyfill 提供，不提交 `node_modules`。
- 网络、文件、JS 执行和图片解码放在后台 goroutine。UI 渲染读取带锁的状态快照，通过事件、世代号和重绘请求更新界面。
- 收藏、历史和歌单使用内存缓存，绘制过程中不访问磁盘。
- 保持现有配置、插件和备份格式的兼容性；行为变化同步更新对应文档。

## 构建与验证

本项目当前不维护测试套件。不要新增或提交测试文件、测试方法、夹具、专用测试程序和仅供测试使用的辅助代码，不运行 `go test`。这属于本项目的维护约定，不是通用的开源项目要求。

按改动范围做最少且有效的验证。代码修改后，在项目根目录构建唯一启动程序：

```sh
CGO_ENABLED=0 go build -trimpath -tags mygo_noinspector -ldflags "-s -w" -o build/musicgo ./cmd/musicgo
```

Windows 使用 [README](README.md) 中的 PowerShell 命令，输出到 `build/musicgo.exe`。不要在根目录或 `bin/` 下生成另一份 MusicGo 可执行文件。

需要静态检查时使用：

```sh
CGO_ENABLED=0 go vet -tags mygo_noinspector ./cmd/musicgo ./internal/...
```

自动验证必须静默，不得打开真实音频输出设备或播放声音；音量为零也不等于静默验证。确需运行验证时使用隔离的临时数据，不读取个人插件、不访问真实音源、不修改用户数据。普通启动会加载内置插件并可能导入已有插件，因此不要把直接启动应用当作隔离验证。

提交说明应写清改动、验证命令和实际验证范围。构建成功不能表述为真实播放或跨平台交互已验证。

## 提交范围

提交主程序、业务源码、实际使用的嵌入资源、`go.mod` / `go.sum` 及维护文档。`internal/plugin/builtin/*.js` 是程序需要嵌入的插件源码，必须保留。

不提交个人配置、账号 Cookie / Token、数据库、下载音乐、缓存、构建产物、本地工具链、测试、临时原型、演示程序及无关报告。发布二进制时使用托管平台的 Release 附件。

根目录、`cmd/` 和 `docs/` 使用 `.gitignore` 白名单。新增正式文件时同步加入白名单；不要用 `git add -f` 绕过忽略规则。提交前检查：

```sh
git status --short
git diff --cached --stat
git diff --cached
```

## 问题反馈

请提供系统和桌面环境、Go / MusicGo 版本、复现步骤、预期行为与实际结果。涉及插件时注明插件名称与版本。日志和截图先移除账号信息、Cookie、Token、私人路径及带鉴权参数的音源地址，不上传个人数据目录。

项目的原创代码采用 [MIT License](LICENSE)。请仅提交你有权提供的代码与素材；引入第三方内容时保留其版权和许可声明，并同步更新 [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md)。提交贡献即表示你同意按本项目的 MIT 许可提供该贡献；第三方内容继续遵循其原有许可。
