# ClipCascade v1.14 - Android 分享文件同步与桌面文件剪贴板兼容性更新

## 发布说明

本次更新围绕 Android 分享入口、跨设备文件同步、图片预览、服务端文件中转稳定性，以及 Windows/macOS/Linux 桌面剪贴板兼容性做了一轮修复。

## 修复内容

### [fix] Android 分享入口改为后台处理

- 新增独立透明 `ShareActivity`
- 分享文本、图片、文件时不再拉起主页面
- `MainActivity` 回归主界面职责，分享逻辑从主界面拆出

### [fix] Android 支持 25 MiB 以下文件分享同步

- Android 分享入口支持 `*/*` 文件类型
- 25 MiB 以下文件上传到服务端 HTTP relay，再同步给桌面端
- 超过 25 MiB 的文件在 Android 端直接拒绝自动发送
- 修复 APK、视频等分享导致服务端 multipart panic 的问题

### [fix] Android 图片接收后点击改为本机预览

- 历史记录里的图片点击后在应用内预览
- 不再弹出系统“打开/发送”选择器
- 预览时按屏幕尺寸下采样，降低大图 OOM 风险

### [fix] Android 分享图片不再通过 Intent 传大 base64

- 分享图片先保存到本地缓存，再由后台服务读取并发送
- 避免 Android Binder 大对象限制导致只写入本机历史、远端收不到
- 发送成功后再写入本机历史，减少“假成功”

### [fix] Android 分享图片可在 Windows 正常粘贴

- Windows 接收远端 `image` 时不再只按 PNG 解码
- 支持 Android 分享入口常见的 JPEG/WebP/GIF/PNG 图片载荷
- 接收后统一写入 Windows 图片剪贴板格式（`CF_DIB`、`CF_DIBV5`、PNG 注册格式）
- 保持“图片就是图片剪贴板”的语义，不降级成文件路径粘贴

### [fix] Android 发送历史即时刷新

- 分享文件成功后主动广播历史变化
- 主界面 `onResume` 会重新读取本地历史
- 修复 Android 端分享发送后历史卡片不刷新的问题

### [fix] Android API session 持久复用

- API session cookie 持久化到本地配置
- 分享文件、刷新文件列表等接口优先复用现有 session
- 只有遇到 `401/403` 才重新登录

### [fix] 服务端文件上传稳定性

- `/api/files/upload` 改用 Fiber 原生 multipart 解析
- 增加 upload handler 局部 recover 和全局 recover 中间件
- 文件上传完成后打印真实文件名、relay id、字节数和人类可读大小
- 避免 E2EE 下只能看到 `file_offer` envelope 大小而误判文件大小

### [fix] 桌面端文件回填处理

- macOS 保留 changeCount 防回写，避免远端写入剪贴板后立刻再次发送
- Windows 端不再启用 changeCount 拦截，按实际兼容性允许回填
- 文件 hash 对单文件使用内容和大小计算，减少路径格式差异造成的误判

### [fix] Windows/Linux 文件剪贴板兼容性

- Windows 接收远端文件时会保存到临时目录，并写入 `CF_HDROP` 文件剪贴板
- 修复视频、压缩包等文件已接收但 `Ctrl+V` 无法粘贴的问题
- Windows 端允许图片/文本等系统剪贴板回填，优先保证能接收到内容
- Linux 文件剪贴板支持 Wayland `wl-copy/wl-paste`，X11 兜底 `xclip`
- Linux 写入改为同步等待，不再异步启动后假成功

## 影响范围

- **Android 原生客户端**：分享入口、图片预览、文件上传、历史刷新、API session 复用
- **Windows 桌面端**：图片按图片剪贴板写入；文件按 `CF_HDROP` 文件剪贴板写入，支持直接粘贴
- **macOS 桌面端**：保留远端写入防回写
- **Linux 桌面端**：Wayland/X11 文件剪贴板路径更新
- **服务端**：文件上传稳定性和上传完成日志增强

## 编译验证

```bash
go test ./client-desktop/... ./server/...
GOOS=windows GOARCH=amd64 go test -c ./client-desktop/clipboard -o /tmp/clipcascade-clipboard.test.exe
GOOS=linux GOARCH=amd64 go test -c ./client-desktop/clipboard -o /tmp/clipcascade-clipboard-linux.test

cd client-android-native-shell/android
./gradlew testDebugUnitTest assembleDebug
```

---

*记录人：Codex*
