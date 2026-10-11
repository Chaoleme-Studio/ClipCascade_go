# ClipCascade 版本升级检查清单

本清单用于每次正式版本发布，当前记录的发布目标为 `v2.4`。

## 版本入口

| 文件 | v2.4 状态 |
|------|------|
| `core/constants/constants.go` | `AppVersion = "2.4"` |
| `client-desktop/main.go` | `Version = "2.4"` |
| `client-desktop/cmd/desktop-ui/main.go` | `Version = "2.4"` |
| `client-android-native-shell/android/app/build.gradle.kts` | `versionCode = 24`, `versionName = "2.4"` |
| `Makefile` | `VERSION ?= 2.4` |
| `_docs/STATUS.md` | `版本：v2.4` |
| `.github/workflows/build.yml` | Release 正文使用 `CHANGELOG-2.4.md` |
| `CHANGELOG-2.4.md` | 已记录本次功能、兼容性和验证结果 |

## 发布前验证

```bash
go test ./core/... ./server/... ./client-desktop/... ./client-mobile/... ./server-web-clip/...
go test -race ./core/... ./server/... ./client-mobile/...
go vet ./core/... ./server/... ./client-mobile/...
git diff --check
bash build-scripts/build_android.sh
cd client-android-native-shell/android
./gradlew --no-daemon testDebugUnitTest lintDebug
```

确认 Android APK：

```bash
aapt dump badging build/ClipCascade-Android-Installable.apk | head
apksigner verify --verbose build/ClipCascade-Android-Installable.apk
```

确认 Linux 产物（CI 构建完成后下载）：

```bash
# 应只列出 <= GLIBC_2.28 的符号版本，出现更高版本说明构建环境漂移了
objdump -T clipcascade-desktop-linux-amd64 | grep -oE 'GLIBC_2\.[0-9]+' | sort -uV
ldd clipcascade-desktop-linux-amd64
```

Linux 产物由 `build-desktop-linux-legacy` / `build-desktop-ui-linux-legacy` 两个 job
在 Debian 10 容器内构建，构建脚本 `build-scripts/build_linux_legacy.sh` 已内置
GLIBC 符号版本守卫，超标会直接让 job 失败。

## GitHub 发布顺序

1. 提交 `CHANGELOG-2.4.md`、状态文档和构建配置。
2. 推送 `main`。
3. 创建并推送 `v2.4` Tag。
4. 等待 `.github/workflows/build.yml` 的 Tag 工作流完成。
5. 确认 Release 附件包含服务端、桌面端、桌面 UI、Web Clip 和 Android APK。
6. 在统信 UOS / deepin 真机上启动 `clipcascade-desktop-linux-amd64`，确认托盘与剪贴板同步正常。

插件兼容声明 `>=2.0.0` 属于插件 API 最低兼容版本，不随应用显示版本自动改成 `>=2.4.0`。
