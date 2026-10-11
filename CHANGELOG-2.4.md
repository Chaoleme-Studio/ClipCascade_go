# ClipCascade v2.4 — 同步可靠性与服务端加固

本版本整合 v2.2 的 Linux glibc 2.28 兼容构建和 v2.3 的 Linux 文件剪贴板修复，并改进 Android、桌面端与服务端的稳定性。

## 剪贴板同步

- Android 后台服务在网络恢复或引擎启动失败后自动重试；读取剪贴板前等待窗口焦点，发送时使用带超时的 WakeLock。
- 移动引擎与桌面端为断线期间的普通剪贴板消息保留待发队列，连接恢复后补发。补发失败会保留剩余消息；E2EE 密钥未就绪时不会发送明文。
- 消息增加 UUID、设备 ID 和时间戳，桌面端与移动端据此过滤回环及重复投递。
- 桌面端 P2P 信令自动重连，分片接收加入边界、数量和过期检查。
- 桌面端文件下载采用唯一目标路径，历史图片与延迟下载映射增加清理和持久化处理。

## 服务端与 WebClip

- WebSocket 增加消息大小和单用户连接数限制；默认只允许同源浏览器连接，跨域使用 `CC_ALLOWED_ORIGINS` 显式配置。
- API 增加每 IP 限流，可用 `CC_API_RATE_LIMIT` 调整或关闭；默认会话有效期从约一年改为七天，可用 `CC_SESSION_TIMEOUT` 覆盖。
- 用户状态与角色在请求时回查数据库；新用户使用随机 E2EE salt，已有用户的 salt 不变。
- SQLite 启用 WAL 与写连接串行化；WASI 插件支持通过 `CC_PLUGIN_SHA256` 校验白名单。
- WebClip 可设置 `WEBCLIP_PASSWORD` 开启访问口令；上传文件按类型决定内联预览或下载，阻止 HTML/SVG 同源执行。

## 构建

- `Makefile` 作为 `build-scripts/build.sh` 的统一入口；应用版本、Android `versionCode` 和 Release 正文更新至 2.4。
- Linux 构建继续在 Debian 10 容器内完成，保留 glibc 2.28 兼容性与 UOS/deepin 文件剪贴板修复。

## 已知限制

- 桌面端断线期间复制文件路径时，需要连接恢复后重新复制该文件；文件上传或登记依赖在线服务端。

## 发布前验证

- core、server、client-desktop、client-mobile 和 server-web-clip 的 Go 测试通过；CI 同款 `go test -race` 与 `go vet` 通过。
- Android `testDebugUnitTest`、`lintDebug` 通过；Go 引擎 AAR 与 Debug/Release APK 构建通过。
- 可安装 APK 的 `versionCode=24`、`versionName=2.4`，APK 签名验证通过。
