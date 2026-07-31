# ClipCascade 版本升级检查清单

本清单用于每次正式版本发布，当前记录的发布目标为 `v2.1`。

## 版本入口

| 文件 | v2.1 状态 |
|------|------|
| `core/constants/constants.go` | `AppVersion = "2.1"` |
| `client-desktop/main.go` | `Version = "2.1"` |
| `client-desktop/cmd/desktop-ui/main.go` | `Version = "2.1"` |
| `client-android-native-shell/android/app/build.gradle.kts` | `versionCode = 21`, `versionName = "2.1"` |
| `Makefile` | `VERSION ?= 2.1` |
| `_docs/STATUS.md` | `版本：v2.1` |
| `.github/workflows/build.yml` | Release 正文使用 `CHANGELOG-2.1.md` |
| `CHANGELOG-2.1.md` | 已记录本次功能、兼容性和验证结果 |

## 发布前验证

```bash
go test ./...
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

## GitHub 发布顺序

1. 提交 `CHANGELOG-2.1.md`、README、状态文档和构建配置。
2. 推送 `main`。
3. 创建并推送 `v2.1` Tag。
4. 等待 `.github/workflows/build.yml` 的 Tag 工作流完成。
5. 确认 Release 附件包含服务端、桌面端、Web Clip 和 Android APK。

插件兼容声明 `>=2.0.0` 属于插件 API 最低兼容版本，不随应用显示版本自动改成 `>=2.1.0`。
