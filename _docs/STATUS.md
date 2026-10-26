# ClipCascade Go — 项目状态文档

> 最后更新：2026-10-27  版本：v2.6

## v2.6 发布重点

- HTTP 中转和延迟下载的单文件上限统一由服务端决定，默认仍为 2 GiB。启动时可用 `--file-relay-max-size 20GiB`，也可设置 `CC_FILE_RELAY_MAX_BYTES`；启动参数优先。
- 新增受认证保护的 `/api/files/config`，桌面端和 Android 在传输前读取当前限制；移除客户端写死的 2 GiB 拦截。旧服务端不支持此接口时回退为 2 GiB。
- Android 在复制大文件源之前检查上限，复制过程中继续检查实际大小；网页展示实际配置上限。
- `temp/`、`TEMP/` 和本地 WebClip 编译产物加入忽略列表；保留 v2.5 的 Linux 后端与连接恢复改进。
- Go 全量测试、相关竞态和静态检查、Android 单元测试 / lint / 打包 / APK 版本与签名校验通过；实际启动验证默认、环境变量和参数优先级。20 GiB 边界通过元数据验证，未实际传输 20 GiB 文件。

## v2.5 发布重点

- Linux 主桌面端升级至 `golang.design/x/clipboard v0.11.0`，内置 Wayland / X11 协议访问。自动探测 data-control 能力，不再依赖 `wl-copy`、`wl-paste`、`xclip` 或 `xsel`。
- 文件写入同时发布标准 URI、GNOME 与 MATE 文件格式，保留 KDE、DDE/UOS 等文件管理器的粘贴兼容；原生读写串行保护避免多格式旧事件覆盖远端粘贴的回环守卫。
- 桌面端流式消费 mDNS 结果，保存地址与发现并行、最多四个候选同时登录，成功后取消其他请求；登录失败进入单个重连循环，手动断开可取消正在进行的连接。
- Android 14+ 并行解析发现的服务、优先选择同网段地址；已有结果直接展示，长按重新扫描。修复合法私网网段被误过滤的问题，过滤代理和容器接口。
- Android 补齐无障碍文本选择事件，合并后台读取请求；引擎未就绪时保留最新待发文本，网络写入使用工作线程。网络恢复唤醒重连，移动引擎增加 Ping/Pong 和握手、读写截止时间。
- [README 的 Linux 安装说明](../README.md#linux-运行时依赖) 按桌面列出接口支持、按发行版列出运行库安装命令，并区分系统剪贴板、历史管理器和可选诊断工具。

### 验证与支持范围

- core、desktop 的 app/clipboard/transport/config/transfer、mobile 的 engine/bridge 的 Go 测试、竞态检查和相关 `go vet` 已通过。
- Fedora KDE/Wayland 在清空 `PATH` 和 `DISPLAY` 后的只读测试通过；独立 KWin/Wayland 的文本、图片、文件及回环测试连续五轮通过，独立 XWayland 显示上的同类测试也通过。测试没有替换用户桌面的剪贴板。
- Windows 剪贴板包交叉编译通过；Linux 托盘产物在 Debian 10 构建并通过 glibc 2.28 符号守卫。
- 完整跨平台产物、Android AAR/APK、Android 单元测试与 lint 由 v2.5 标签工作流构建和验证。Android 真机后台行为尚需设备实测。
- 原生后端覆盖 `client-desktop` 及其图形面板；`client-mobile` 的 Fyne 演示桌面产物仍使用 Fyne 剪贴板接口。GNOME、XFCE、MATE、DDE 等完整桌面的逐项实测范围见 README。

### 已知限制与后续方向

- Android 10+ 后台剪贴板读取受窗口焦点限制，前台服务、无障碍和电池白名单本身不等于读取权限；悬浮窗方案仍需按设备系统验证。
- 手机后台目前只重连保存的服务器地址，服务器 DHCP 换 IP 后的后台重新发现尚未实现。
- 旧 Android NSD 使用 legacy 串行解析接口；解析超时与取消仍需分版本完善。
- 无 data-control 且无可访问 XWayland 的 Wayland 会话无法后台读取剪贴板；额外安装历史管理器不会增加该系统接口。
- 路由器/AP 隔离、VPN 路由或组播策略可能阻断 mDNS；服务器应避免广播仅本机可达的虚拟网卡地址。

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
