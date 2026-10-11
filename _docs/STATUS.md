# ClipCascade Go — 项目状态文档

> 最后更新：2026-10-11  版本：v2.4

## v2.4 发布重点

- Android 后台服务增加剪贴板读取与引擎重试、网络恢复感知和短时 WakeLock；移动引擎断线后保留并补发消息。
- 桌面端增加消息 UUID 去重、P2P 信令重连和分片边界检查，断线期间的普通剪贴板消息可在连接恢复后补发。
- 服务端加强 WebSocket、会话、跨域、API 限流和插件校验；WebClip 增加可选访问口令与受控文件下载。
- 统一构建入口，并沿用 v2.2/v2.3 的 Linux glibc 2.28 兼容构建与文件剪贴板修复。

## v2.3 发布重点

- 修复 Linux 文件剪贴板在统信 UOS / deepin 上**粘贴不出文件**的问题：`dde-file-manager` 只认 `x-special/gnome-copied-files`，写入 target 改为按桌面环境选择（KDE 用 `text/uri-list`，MATE 用 `x-special/mate-copied-files`）。
- 修复 `xclip` **阻塞接收线程**的问题：属主进程 stdio 指向 `/dev/null`，改用 `Start()` + 异步 `Wait()`，不再等待剪贴板被其它程序接管。
- 读取文件剪贴板时依次尝试多个 target，清洗 gnome 系 `copy\n` 前缀，并用 `os.Stat` 校验 `file://` 路径是否真实存在。
- README 补充文件剪贴板 target 的桌面环境差异说明。

## v2.2 发布重点

- 修复 Linux 桌面包在统信 UOS V20、deepin 20 等 glibc 2.28 系统上无法启动的问题（原先产物硬绑 `GLIBC_2.32` / `GLIBC_2.34`）。
- Linux 桌面端与 Fyne 桌面 UI 产物改为在 Debian 10 容器内编译，只要求 glibc ≥ 2.28，同时兼容所有更新的发行版；产物文件名不变。
- `build-scripts/build_linux_legacy.sh` 内置 GLIBC 符号版本守卫，产物要求高于 2.28 时直接让 CI 失败。
- README 补充 Linux 运行时依赖清单。

## 目录结构

```
ClipCascade_go/
├── server/                        # 服务端：Fiber + WebSocket + 用户认证
├── client-desktop/                # 桌面端：系统托盘 + 剪贴板监听
├── client-mobile/                 # Fyne UI 客户端（桌面/移动）
├── client-android-native-shell/   # Android 原生 Kotlin 壳（Gradle）
├── core/                          # 共享库：加密、协议、常量
├── server-web-clip/               # Web 网页剪贴板（独立子模块）
├── build-scripts/                 # build.sh / build_android.sh / build_linux_legacy.sh
├── _docs/screenshots/             # README 截图
├── build/                         # 编译产物（gitignore）
└── go.work                        # Go workspace
```

## 模块信息

| 目录 | go.mod 模块名（历史遗留） | 说明 |
|------|--------------------------|------|
| server | github.com/clipcascade/server | 服务端 |
| client-desktop | github.com/clipcascade/desktop | 桌面客户端 |
| client-mobile | github.com/clipcascade/fynemobile | Fyne UI |
| core | github.com/clipcascade/pkg | 共享库 |
| server-web-clip | github.com/clipcascade/server-web-clip | Web 剪贴板 |

## server-web-clip

- Fiber + glebarez/sqlite，CGO_ENABLED=0，单二进制 ~14MB
- 模板 embed 进二进制
- 数据默认存放在**二进制所在目录**（非 cwd）
- 启动时打印 database/uploads 绝对路径

| 环境变量 | 默认 |
|---------|------|
| WEBCLIP_PORT | 8090（占用自动+1）|
| WEBCLIP_DB_PATH | 二进制目录/data.db |
| WEBCLIP_UPLOAD_DIR | 二进制目录/uploads |
| WEBCLIP_PASSWORD | 空（不启用访问口令）|

## 构建命令

```bash
./build-scripts/build.sh server
./build-scripts/build.sh server-cross
./build-scripts/build.sh desktop
./build-scripts/build.sh cross             # server + desktop + web-clip 全平台
./build-scripts/build.sh web-clip-cross
./build-scripts/build.sh mobile-android-native
./build-scripts/build.sh all
```

## CI 产物（GitHub Actions）

| 产物 | 来源 job |
|------|---------|
| clipcascade-server-{os}-{arch} | build-server |
| clipcascade-desktop-{os}-{arch} | build-desktop（linux-amd64 由 build-desktop-linux-legacy 提供）|
| clipcascade-ui-desktop-{os}-{arch} | build-desktop-ui-native（linux-amd64 由 build-desktop-ui-linux-legacy 提供）|
| clipcascade-web-{os}-{arch} | build-web-clip |
| ClipCascade-Android-Installable.apk | build-mobile-native |

## 本地环境（~/.zshrc 已配置）

```bash
ANDROID_HOME=/opt/homebrew/share/android-commandlinetools
ANDROID_NDK_HOME=/opt/homebrew/Caskroom/android-ndk/29/AndroidNDK14206865.app/Contents/NDK
```

## Dockerfile 说明

`server/Dockerfile` 使用 `GOWORK=off`，只复制 `core/` 和 `server/`，
不需要其他模块，避免 go.work 引用缺失报错。
