# ClipCascade v2.1 - Android 文件接收、公共下载与局域网发现

## 发布说明

v2.1 重点完善 PC 到 Android 的文件接收体验，同时保留 Android 平台对系统剪贴板文件能力的限制边界。PC 复制图片、小文件、中等文件或大文件后，Android 都能在正确的历史入口看到对应内容，并在用户需要时保存到公共 `Download` 目录。

本版本也修复了 Android 局域网发现按钮不重扫、IP 变化反应慢和 mDNS stop/start 竞态等问题，并将正式版本统一到 `2.1`。

## 文件与图片同步

### [feature] Android 支持 PC `file_eager` 小文件

- 移动 Go 引擎和 gomobile bridge 回调携带原始文件名，避免 Android 只收到 Base64 内容而丢失文件名。
- Android 原生后台服务处理 `file_eager`，按 `8 MiB` 协议上限解码到应用缓存。
- 小文件只保留最近一个临时副本；新文件到达时替换旧缓存和旧的小文件历史项。
- 接收文件不会强行写入 Android 系统剪贴板，因为 Android 没有稳定的系统文件剪贴板写入接口。

### [feature] Android 图片和文件保存到公共 Download

- 历史图片增加保存入口，使用 `MediaStore.Downloads` 写入公共 `Download/` 目录。
- `file_eager` 小文件可以从剪贴板历史直接保存到 `Download/`。
- `file_offer` 历史记录保存文件名、大小、MIME 类型和 relay ID；点击时才下载并保存，不长期复制文件内容。
- Android deferred 大文件下载同样写入公共 `Download/`，不再依赖难以在文件管理器中找到的应用私有路径。
- Android 低于 API 29 时使用公共 Download 目录和 MediaScanner 兼容路径。

### [compatibility] 统一文件分级

所有端共享以下协议分级：

- `<=8 MiB`：`file_eager` Base64 直传。
- `>8 MiB 且 <=25 MiB`：HTTP relay 自动中继并广播 `file_offer`。
- `>25 MiB 且 <=2 GiB`：只登记 deferred 元数据，接收端请求后由源设备上传。
- `>2 GiB`：拒绝自动传输。

Android 分享发送端因为系统提供的是 `content://` 文件源，`<=25 MiB` 仍使用 HTTP relay；这是 Android 文件源的专用实现，不改变 PC 端和 Android 接收端对上述三档协议的理解。

## [fix] Android 局域网发现

- 点击“发现”现在总会清除旧结果并真正开始一次新扫描。
- 发现结果到达后自动弹出可选服务器列表，不再要求先长按或重复点击空按钮。
- 发现按钮在没有结果时保持可用，显示“扫描中”和结果数量。
- 使用串行 mDNS 服务解析，避免 `NsdManager.resolveService` 并发调用导致漏发现。
- 等待 `onDiscoveryStopped` 完成后才启动下一次扫描，消除 stop/start 竞态。
- 监听当前网络接口和 IP 地址变化，网络切换或地址变化后防抖重启发现并清理旧 URL。
- Activity 退到后台时停止 NSD 发现，回到前台后重新开始，避免无意义的常驻发现流量和耗电。

## [fix] 移动消息桥与 E2EE 控制消息

- 移动消息回调增加文件名元数据，兼容 `file_eager`、`file_offer`、`file_request`。
- E2EE 开启时允许正确处理未加密的服务端文件控制消息，同时仍对加密剪贴板消息执行解密。
- 图片和小文件接收改为单线程顺序处理，保证“最近一个”缓存和历史记录一致。

## [chore] 版本与构建

- 服务端、桌面托盘端、桌面 UI 端版本统一为 `2.1`。
- Android `versionName` 为 `2.1`，`versionCode` 为 `21`。
- GitHub Tag Release 使用本文件作为发布说明，并自动附带各平台构建产物和 Android 可安装 APK。

## 功耗与存储边界

- 小文件不会启动周期性下载或保活任务，只在收到消息时解码一次。
- Android eager 小文件最多保留一份，文件内容不会随剪贴板历史无限累积。
- mDNS 发现只在主界面前台运行，网络变化才触发重新扫描。
- 大文件只在登记、用户请求下载或源设备被请求上传时读写，不会在后台预先传输。

## 验证

- `go test ./...`：`client-mobile`、`client-desktop`、`server` 通过。
- Android `testDebugUnitTest` 和 `lintDebug` 通过。
- `build-scripts/build_android.sh` 成功生成 debug/release APK。
- APK 元数据确认 `versionName=2.1`、`versionCode=21`，APK v2 签名校验通过。
- 当前发布前环境没有连接 Android 真机，PC → Android 的最终实机交互仍需安装后验证。

---

*记录人：Codex*
