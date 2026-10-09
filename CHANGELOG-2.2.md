# ClipCascade v2.2 - Linux 发行版兼容性修复

## 发布说明

v2.2 修复 Linux 桌面包在旧 glibc 发行版上无法启动的问题。

此前 CI 的 `linux-amd64` 产物在 `ubuntu-latest`（当前为 Ubuntu 24.04，glibc 2.39）上带 CGO 编译，产出的二进制硬绑 `GLIBC_2.32` / `GLIBC_2.34` 符号版本。在统信 UOS V20、deepin 20 这类基于 Debian 10（glibc 2.28）的系统上会直接启动失败：

```
./clipcascade-desktop-linux-amd64: /lib/x86_64-linux-gnu/libc.so.6:
version `GLIBC_2.32' not found (required by ./clipcascade-desktop-linux-amd64)
./clipcascade-desktop-linux-amd64: /lib/x86_64-linux-gnu/libc.so.6:
version `GLIBC_2.34' not found (required by ./clipcascade-desktop-linux-amd64)
```

本版本把 Linux 产物改为在 Debian 10（glibc 2.28）容器内编译。glibc 向后兼容，因此新产物在所有 glibc ≥ 2.28 的发行版（Debian 10+、Ubuntu 18.10+、统信 UOS V20、deepin 20 及以上）上都能运行，覆盖面比原来更广。

## [fix] Linux 桌面端 glibc 兼容性

- `clipcascade-desktop-linux-amd64`（隐形式托盘端）改为在 `debian:buster` 容器内编译，产物只要求 glibc ≥ 2.28。
- 该产物依赖 CGO，无法纯静态编译：`getlantern/systray` 需要 GTK3 + appindicator，`golang.design/x/clipboard` 编译期需要 `<X11/Xlib.h>`。
- 托盘后端链接 `libayatana-appindicator3`：统信 UOS V20 只提供 `libayatana-appindicator3.so.1`，没有 `libappindicator3.so.1`。Debian 10 自带 `libayatana-appindicator3-dev`，所以 glibc 仍然是 2.28，无需降级或打包 glibc。
- 产物文件名保持不变，Release 资产名与既有下载链接不受影响。

## [fix] Linux 图形化桌面 UI glibc 兼容性

- `clipcascade-ui-desktop-linux-amd64`（Fyne 面板）存在同样的问题，改为同样在 Debian 10 容器内编译。
- Fyne 在 Linux 上同样依赖 CGO（X11 + OpenGL，`go-gl/glfw` 会一并编译 GLFW 的 C 源码），之前经由 `fyne-cross` 在 Ubuntu 环境构建。
- 确认 Fyne v2.7 不依赖 webkit，`fyne.io/systray` v1.12 在 Linux 上是纯 Go 的 D-Bus 实现，因此该产物不需要 appindicator 与 webkit2gtk 运行库。

## [build] 构建流程

- 新增 `build-scripts/build_linux_legacy.sh`，在 Debian 10 容器内完成依赖安装、Go 工具链准备和编译，支持 `desktop` 与 `desktop-ui` 两个目标。
- Go 官方 linux 工具链本身是纯静态链接的（无 `ld.so` interp、无 `GLIBC_*` 符号依赖），可直接在 glibc 2.28 上运行，无需为它单独准备老版本 Go。
- CI 新增 `build-desktop-linux-legacy` 与 `build-desktop-ui-linux-legacy` 两个 job，并从原 `build-desktop` / `build-desktop-ui-native` 矩阵中移除 linux 条目。
- 构建脚本内置守卫：编译完成后用 `objdump -T` 检查产物的 GLIBC 符号版本要求，只要高于 `2.28` 就判定失败，避免后续配置漂移产出不可用二进制。

## [docs] 文档

- README 增加 Linux 运行时依赖说明，按隐形式桌面端和图形化桌面 UI 分别列出所需系统库、各自缺失后的影响，以及 glibc 版本要求。

## [chore] 版本与构建

- 服务端、桌面托盘端、桌面 UI 端版本统一为 `2.2`。
- Android `versionName` 为 `2.2`，`versionCode` 为 `22`。
- GitHub Tag Release 使用本文件作为发布说明，并自动附带各平台构建产物和 Android 可安装 APK。

## 验证

- `.github/workflows/build.yml` 通过 YAML 解析校验，job 依赖关系与 Release `needs` 已同步更新。
- `build-scripts/build_linux_legacy.sh` 通过 `bash -n` 语法检查。
- 版本号入口与 `_docs/VERSION_BUMP_CHECKLIST.md` 清单一致。
- Linux 产物由 CI 在 Debian 10 容器内构建，并由构建脚本内的 GLIBC 守卫校验符号版本上限。
- 本机（Windows）无可用 Docker/WSL2 环境，容器构建过程未在本地实跑；统信 UOS 真机启动验证待安装产物后进行。

---

*记录人：Claude*
