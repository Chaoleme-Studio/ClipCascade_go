# ClipCascade Go — 项目状态文档

> 最后更新：2026-08-01  版本：v2.1

## v2.1 发布重点

- Android 原生端完整识别 PC 发来的 `file_eager`、`file_offer` 和 deferred 文件消息。
- PC 小文件在 Android 端只保留最近一个临时副本，用户点击后保存到公共 `Download` 目录。
- Android 接收图片和中继文件支持保存到公共 `Download` 目录，不再依赖应用私有路径。
- 文件传输上限统一记录为 `8 MiB / 25 MiB / 2 GiB` 三档；Android 的 `content://` 分享发送仍使用专用 HTTP 中继路径。
- Android 局域网发现支持真正重扫、网络/IP 变化重启发现，以及前后台生命周期管理。

## 目录结构

```
ClipCascade_go/
├── server/                        # 服务端：Fiber + WebSocket + 用户认证
├── client-desktop/                # 桌面端：系统托盘 + 剪贴板监听
├── client-mobile/                 # Fyne UI 客户端（桌面/移动）
├── client-android-native-shell/   # Android 原生 Kotlin 壳（Gradle）
├── core/                          # 共享库：加密、协议、常量
├── server-web-clip/               # Web 网页剪贴板（独立子模块）
├── build-scripts/                 # build.sh / build_android.sh
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
| clipcascade-desktop-{os}-{arch} | build-desktop |
| clipcascade-ui-desktop-{os}-{arch} | build-desktop-ui-native |
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
