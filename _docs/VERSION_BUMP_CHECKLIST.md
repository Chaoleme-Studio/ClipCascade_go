# ClipCascade 版本升级检查清单

> 目标：将项目内所有版本相关硬编码、构建配置、文档统一更新到 v1.14。

## 已确认已更新（无需再动）

| 文件 | 状态 |
|------|------|
| `CHANGELOG-1.14.md` | 已存在，标题为 `ClipCascade v1.14 ...` |
| `client-android-native-shell/android/app/build.gradle.kts` | `versionCode = 14`, `versionName = "1.14"` |
| `.github/workflows/build.yml` release job | `body_path: CHANGELOG-1.14.md` |

## 本次需要更新的项

### 1. 服务端硬编码版本（必须）
- **文件**：`core/constants/constants.go:51`
- **当前**：`AppVersion = "0.1.0"`
- **改为**：`AppVersion = "1.14"`
- **说明**：该常量通过 `/server-version` 接口返回，是服务端对外暴露的版本号。

### 2. PC 桌面端增加版本变量（必须）
- **文件**：`client-desktop/main.go`
- **当前**：无 `Version` 变量
- **改为**：在 `package main` 下添加 `var Version = "1.14"`
- **说明**：`build-scripts/build.sh` 和 `Makefile` 都通过 `-X main.Version=...` 注入版本，但 desktop 的 `main` 包原本没有该变量，注入无效。

### 3. Makefile 默认版本（必须）
- **文件**：`Makefile:7`
- **当前**：`VERSION ?= 0.1.0`
- **改为**：`VERSION ?= 1.14`

### 4. 项目状态文档版本（建议）
- **文件**：`STATUS.md:3`
- **当前**：`版本：v1.3`
- **改为**：`版本：v1.14`

### 5. Fyne 桌面 UI / client-mobile 版本（可选）
- **文件**：`client-mobile/main.go`
- **当前**：无版本号
- **建议**：如需在 Fyne UI 显示版本，添加 `var Version = "1.14"` 并在界面或日志中引用。如果本次发布不涉及 UI 版，可跳过。

## 验证方式

1. 构建服务端后访问 `/server-version`，确认返回 `{"version":"1.14"}`。
2. 构建桌面端后检查二进制中是否包含 `1.14`（`strings clipcascade-desktop | grep 1.14`）。
3. 使用 `make server VERSION=1.14` 或 `./build-scripts/build.sh server` 构建，确认产物版本正确。
