MusicGo 首个跨平台二进制版本，使用 Go + MyGo 原生 GPU 渲染，无需 Electron、WebView 或 Node.js。

本次包含截至此版本的播放器与界面更新：QQ 音乐、汽水音乐内置插件及扫码登录，多账号切换，账号与音源发现范围，播放队列、封面切歌预览和主题配色调整。

| 系统 | 架构 | 下载文件 |
| --- | --- | --- |
| Windows 10 / 11 | x64 / ARM64 | `musicgo-setup-版本-amd64.exe` / `musicgo-setup-版本-arm64.exe` |
| macOS 13+ | Intel / Apple Silicon | `musicgo-版本-amd64.dmg` / `musicgo-版本-arm64.dmg` |
| Linux | x64 / ARM64 | `musicgo_版本_架构.deb` 或 `musicgo-版本-linux-架构.tar.gz` |

Windows 运行安装程序；macOS 打开 DMG 并将 MusicGo 拖入 Applications；Debian / Ubuntu 使用 `sudo apt install ./musicgo_*.deb` 安装匹配架构的文件；其他 Linux 发行版可解压压缩包后运行 `./musicgo`。

Linux 需要 GTK 3、libepoxy 和 PulseAudio（或 PipeWire 的 PulseAudio 兼容服务），也可回退 ALSA；托盘需要 Ayatana AppIndicator。推荐各平台安装 FFmpeg / FFprobe 并加入 PATH，以支持更多音频格式；发布包未包含 FFmpeg。

此版本未使用商业代码签名证书，macOS 仅作 ad-hoc 签名，未进行 Apple 公证，Windows 未作 Authenticode 签名，系统可能显示安全提示。插件接口、登录和音源可用性依赖第三方服务。

所有发布目标使用 `CGO_ENABLED=0` 构建并按系统打包。构建和打包成功不代表已完成各系统的实机界面、扫码登录或真实播放验证；本次自动构建不会启动播放器或打开音频设备。`SHA256SUMS` 用于核对附件完整性。
