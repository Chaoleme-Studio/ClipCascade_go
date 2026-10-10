# ClipCascade v2.3 - Linux 文件剪贴板修复

## 发布说明

v2.3 修复 Linux 文件剪贴板的两类问题：在统信 UOS / deepin 上粘贴不出文件，
以及接收线程被 `xclip` 阻塞卡死。本版本建立在 v2.2 的 glibc 2.28 兼容构建之上，
其余平台与功能不受影响。

两个问题的成因：

1. **粘贴不出文件**：统信 UOS / deepin 的 `dde-file-manager` 只监听
   `x-special/gnome-copied-files`（对应其源码里的 `kGnomeCopyKey` 常量），
   完全忽略 `text/uri-list`。此前写剪贴板一律只写 `text/uri-list`，
   于是文件管理器能感知到剪贴板更新，`Ctrl+V` 却粘不出任何文件。

2. **接收线程卡死**：`xclip` 默认 fork 到后台持有 selection，但不关闭继承来的
   stdout/stderr。原实现用 `CombinedOutput()` 捕获输出，而管道要等子进程退出才会
   EOF，于是调用方一直阻塞到剪贴板被别的程序接管为止——实测可达数十秒，甚至永久。

## [fix] 写入目标按桌面环境选择

- 新增 `detectFileClipboardFormat()`，依据 `XDG_CURRENT_DESKTOP` 决定剪贴板 target 与数据格式。
- 统信 UOS / deepin / GNOME / Unity / XFCE / Cinnamon，以及探测不到桌面环境时：
  统一使用 `x-special/gnome-copied-files`，数据以 `copy\n` 前缀开头，其后每行一个 `file://` URI。
- KDE (Dolphin)：使用 `text/uri-list`。
- MATE：使用 `x-special/mate-copied-files`，同样带 `copy\n` 前缀。
- Wayland 会话下优先 `wl-copy --type <target>`，否则 `xclip -selection clipboard -t <target>`。

## [fix] 不再阻塞接收线程

- 新增 `runClipboardOwner()`：把属主进程的 stdio 指向 `/dev/null`（直接复用 `*os.File`，
  不额外建立管道），用 `Start()` 启动后立即返回，`Wait()` 放到 goroutine 中回收。
- 后台子进程继续持有 selection，调用方不再等待剪贴板被其它程序接管，接收流程不再卡死。

## [fix] 读取方向多目标兜底

- 读取文件剪贴板时依次尝试：当前桌面环境的 target → `text/uri-list` →
  `x-special/gnome-copied-files` → `x-special/mate-copied-files`（去重）。
- 清洗 gnome 系的 `copy\n` / `cut\n` 前缀与结尾 NUL，`file://` URI 先 `url.Parse`
  再用 `os.Stat` 做磁盘校验，只保留真实存在的路径。
- 修复在 DDE 文件管理器中复制文件后发送不出去的问题。

## [test] 单元测试

- 新增 `client-desktop/clipboard/monitor_linux_test.go`，覆盖各桌面环境的 target 选择与负载构造。

## [docs] 文档

- README 的 Linux 依赖表更新 `xclip` 用途说明，并补充文件剪贴板 target 的桌面环境差异
  （含写错 target 时的典型症状：文件管理器能看到剪贴板更新，但 `Ctrl+V` 粘不出文件）。

## [chore] 版本与构建

- 服务端、桌面托盘端、桌面 UI 端版本统一为 `2.3`。
- Android `versionName` 为 `2.3`，`versionCode` 为 `23`。
- GitHub Tag Release 使用本文件作为发布说明，并自动附带各平台构建产物和 Android 可安装 APK。

## 验证

- `go build ./...`（core / server / server-web-clip）与 `go test ./core/... ./server/...` 通过。
- `objdump -T` 检查 Linux 产物最高仍只要求 `GLIBC_2.14`，未影响 v2.2 建立的 glibc 2.28 兼容性。
- 新增的剪贴板单元测试只在 Linux 上编译（`//go:build linux`），且 CI 的 `test` job 只跑
  `./core/... ./server/... ./client-mobile/...`，不含 `client-desktop`，因此该测试不在 CI 中执行；
  需在 Linux 机器上以 `go test ./client-desktop/clipboard/...` 单独验证。
- 统信 UOS 真机上从文件管理器复制/粘贴文件的验证待安装产物后进行。

---

*记录人：Claude*
