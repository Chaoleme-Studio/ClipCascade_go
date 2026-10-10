# ClipCascade Go

# ClipCascade Go

[![English](https://img.shields.io/badge/Language-English-blue?style=for-the-badge)](README_en.md)
[![简体中文](https://img.shields.io/badge/Language-简体中文-red?style=for-the-badge)](README.md)

A cross-platform clipboard synchronization tool designed for multi-device collaboration.
The goal is not to "rebuild another chat tool," but to make cross-device copy-pasting as seamless as a local experience.

## Project Introduction

In daily development and office scenarios, Mac, Windows, Linux, and Android are often used interchangeably.
Transferring text, screenshots, and files between different devices often encounters these issues:

- Need to manually open intermediate tools, interrupting the workflow.
- Large differences in clipboard behavior across platforms, leading to inconsistent experiences.
- File transfer is either too heavy or too slow, and difficult to manage uniformly.

ClipCascade is positioned as a clipboard-centric, self-deployable, extensible, and cross-platform synchronization system.

## Core Capabilities

- **Text Sync**: Real-time synchronization to online devices after copying.
- **Image Sync**: Cross-platform transfer of image clipboard content.
- **File Sync**: Supports a tiered strategy of direct transfer for small files and placeholder notifications for large files.
- **Transfer Channels**: Supports STOMP (server-relayed) + P2P (peer-to-peer) dual channels.
- **Security**: Supports E2EE (End-to-End Encryption) toggle.
- **Server Automation**: Supports rule-triggered audit/tasks and WASI plugin placeholders.
- **Auto-Discovery**: Automatic discovery of service addresses via mDNS in local networks.
- **Multi-User Management**: Administrators can add users directly from the Web page.
- **Android Native Persistence**: Foreground services + accessibility triggers + permission guidance.
- **Clipboard History**: Mobile version supports viewing and re-filling historical records.

## Supported Platforms

| Module | Platform |
| --- | --- |
| Server | Linux / macOS / Windows |
| Desktop (Tray Version) | Linux / macOS / Windows |
| Desktop UI (Fyne) | Linux / macOS / Windows |
| Android Native Persistence | Android |
| Web Clipboard | Linux / macOS / Windows (Single Binary) |

## Current Technical Implementation

This section only describes the technical solutions already implemented in the current repository to facilitate troubleshooting platform differences and behavioral expectations.

### Server-side

- Based on Go + Fiber.
- WebSocket relay uses STOMP-like protocol.
- Database storage uses GORM + SQLite.
- Local network auto-discovery uses mDNS / Zeroconf.
- Supports user management directly via administrator Web page.
- Automation is disabled by default. When enabled, it can match clipboard events and call Go actions, allowlisted local commands, or WASI plugins.

### Desktop Transfer Layer

- Main Channel: STOMP over WebSocket.
- Auxiliary Channel: P2P WebRTC DataChannel.
- Auto-reconnect: The desktop client initiates the first round of reconnection immediately upon detecting disconnection, then enters exponential backoff.
- Current implementation still uses server relay as a fallback; P2P will carry data in parallel when available.

### Desktop Clipboard Implementation

- Text, images, and files are eventually merged into unified business types:
  - `text`
  - `image`
  - `file_eager`
  - `file_stub`
- No separate "in-memory image" protocol type; screenshots, bitmaps, and in-app image clipboard objects are all categorized as `image`.
- To avoid loop triggering when images are re-encoded by the system across platforms, image deduplication is now based on "hash of the decoded pixel content" instead of raw byte hash.

### Platform Clipboard Differences

#### macOS

- Uses CGO native calls for `NSPasteboard`.
- Detects changes via `changeCount` polling, then reads by priority:
  1. File paths
  2. Image objects
  3. Text
- This means macOS can now directly recognize many "in-memory screenshots," such as WeChat screenshots, without needing to paste them elsewhere first.

#### Windows

- Uses system clipboard sequence number to detect changes.
- File paths are read and written natively via `CF_HDROP`. After receiving a remote file, the Windows client writes it to the system file clipboard so it can be pasted directly with `Ctrl+V`.
- Text read natively via `CF_UNICODETEXT`.
- Images are read via `CF_DIB / CF_DIBV5` and converted to PNG before entering the unified `image` sync chain.
- When writing images, remote PNG/JPEG/WebP/GIF payloads are normalized to Windows image clipboard formats (`CF_DIB`, `CF_DIBV5`, and the registered PNG format). Android shared images therefore paste as images, not as file paths.
- This is designed to cover scenarios where "screenshots go directly to the clipboard but cannot be read by common libraries."

#### Linux

- Text/image listening mainly relies on `golang.design/x/clipboard` event channels.
- The file clipboard is read/written via `xclip` (Wayland sessions prefer `wl-copy`/`wl-paste`). **The target is chosen per desktop environment**: UnionTech UOS / deepin / GNOME-family use `x-special/gnome-copied-files` (data format `copy\nfile://...`), KDE uses `text/uri-list`; reads try several targets in order. A wrong target shows up as a file manager that sees a clipboard update but pastes nothing on Ctrl+V.
- Writing to the clipboard forks a background owner process holding the selection. xclip / wl-copy run in the background by default but **do not close the inherited stdout/stderr**, so they must not be captured via `CombinedOutput`/`Output` — otherwise the caller blocks until another program takes over the clipboard.
- Linux currently adheres more closely to "standard clipboard format driven" implementations, with higher dependency on the desktop environment.

### Design Trade-offs

- Text and standard images are "sync on copy" by default.
- Files are not forced to be fully transferred directly; instead, a distinction is made between small file direct transfer and large/multi-file placeholder notifications.
- File handling uses three shared protocol bands: `<= 8 MiB` `file_eager`, `> 8 MiB and <= 25 MiB` HTTP relay, and `> 25 MiB and <= 2 GiB` deferred registration.
- Android cannot reliably write files into the system clipboard. Received `file_eager` data therefore keeps only the newest temporary copy, while `file_offer` keeps download metadata; both are saved to the public `Download` directory only when the user taps save.
- Android received images can be previewed in the app and saved to the public `Download` directory. Temporary caching is event-driven and does not acquire an extra wake lock.
- For screenshot-like content, the current strategy is still to sync as images directly, rather than requiring users to "paste and then copy."
- If a platform's screenshot tool places content in a non-standard private format, further platform adaptation may be needed (this is a clipboard reading layer issue, not a new protocol-level type).

## Quick Start
> Example: Server startup on macOS
![alt text](_docs/screenshots/MacServer.jpg)

> Example: Desktop client startup on macOS
![alt text](_docs/screenshots/MacDesktop.jpg)

> Windows Desktop Client
![alt text](_docs/screenshots/WinDesktop.png)

| Android Client Startup | Desktop UI |
| :---: | :---: |
| <img src="_docs/screenshots/AndroidDesktop.jpg" height="450"> | <img src="_docs/screenshots/DesktopUI.png" height="450"> |

Looking for a version with a UI?
Yes, brother, we've got you covered. The PC version also includes a UI component with built-in network auto-discovery.

To manually specify account and password:
```bash
./build/clipcascade-desktop-ui-darwin-arm64 --server http://127.0.0.1:8080 --username admin --password admin123 --save
```

## Problems it Solves

### 1) Connectivity Issues
Sync directly to another computer without switching software.
Bridge clipboard gaps across different platforms.
No need for strange input methods or login sync.
Privacy-conscious: no internet connection required, local data stays local.

### 2) Android
Many apps struggle to stay in the background on Android 10+ due to battery optimization.
Most regular packaged apps lack this ability and require specialized code (which I have already implemented here).

## Web Clipboard (server-web-clip)

Independent of the ClipCascade account system, fits quick text and file transfers within a local network.

```bash
./build/clipcascade-web-darwin-arm64
```

Prints data path and access address upon startup:

```
  database : /path/to/binary/data.db
  uploads  : /path/to/binary/uploads/
  ➜  http://localhost:8090/
```

**Command Line Arguments:**

```bash
./build/clipcascade-web-darwin-arm64 -p 9000
# or
./build/clipcascade-web-darwin-arm64 --port 9000
```

**Environment Variables (Cli arguments take precedence):**

| env | default | description |
| --- | --- | --- |
| `WEBCLIP_PORT` | `8090` | Port, auto-increments if occupied |
| `WEBCLIP_DB_PATH` | binary dir/data.db | Database path |
| `WEBCLIP_UPLOAD_DIR` | binary dir/uploads | Upload directory |

Build:

```bash
./build-scripts/build.sh web-clip-cross
```

Artifacts: `build/clipcascade-web-{os}-{arch}`

## Executing Build

### 1) One-click Build

```bash
./build-scripts/build.sh all
```

`all` attempts to build:
- server (all platforms)
- desktop (all platforms)
- desktop-ui (current platform)
- desktop-ui-cross (macOS UI uses native dual-arch builds; Windows/Linux UI requires Docker)

Notes:
- Linux desktop cross-compilation requires Docker daemon (will be skipped and warned if not started).
- On macOS hosts, `desktop-ui-cross` still tries to produce `clipcascade-desktop-ui-darwin-amd64` and `clipcascade-desktop-ui-darwin-arm64` even when Docker is not running.
- Windows/Linux Fyne UI cross-compilation requires Docker + `fyne-cross`; it is skipped with a warning when Docker is not running.
- iOS packaging requires Apple developer certificate (warning issued if missing).

### 2) Start Server

```bash
./build/clipcascade-server-darwin-arm64
```

Default credentials: `admin / admin123`

### 3) Start Desktop Client

First-time configuration save:

```bash
./build/clipcascade-desktop-darwin-arm64 --server http://127.0.0.1:8080 --username admin --password admin123 --save
```

Debug logging:

```bash
./build/clipcascade-desktop-darwin-arm64 --debug
```

One-shot send filter (runtime-only, not persisted to config):

```bash
./build/clipcascade-desktop-darwin-arm64 --send-filter=text,file
```

`--send-filter` values: `all` (default), `none`, `text`, `image`, `file`, with comma combinations like `text,file`.

Log size semantics (client vs server):

- With E2EE enabled (default), client-side `size` logs represent estimated plaintext payload size.
- In E2EE mode, server-side `volume` usually reflects encrypted frame body (wire body) size.
- So the same message can show different numbers on client and server; this is expected.
- With E2EE disabled, the two sides are more likely to show close size values.

## User Management

- Default `signup` is disabled.
- `/signup` returns `Signup is disabled` when closed.
- Login page no longer displays "Create an account" when `signup` is off.

Add users (no manual API calls needed):

1. Log in as administrator and open `/advance`.
2. Click `+ Add User`.
3. Enter username and password to create.

## Server Automation and Plugins

Automation is disabled by default and does not affect the existing sync path. With E2EE disabled, the server can inspect plaintext text, URLs, and image metadata. With E2EE enabled, the server only sees envelope metadata.

Plugins use a directory package format. Official lightweight plugins use WASI first, so the same `.wasm` file can run on Linux, macOS, and Windows. Heavy local capabilities should continue to use the `run_command` local command allowlist.

```text
plugins/
  content-redact/
    clipcascade.plugin.jsonc
    README.md
    content-redact.wasm
```

`clipcascade.plugin.jsonc` example:

```jsonc
{
  // publisher 和 name 共同组成插件唯一 ID: example.content-redact
  "publisher": "example",
  "name": "content-redact",
  // displayName 用于网页插件页展示。
  "displayName": "Content Redaction",
  "description": "Detects and redacts sensitive clipboard text.",
  "version": "0.1.0",
  "license": "MIT",
  // categories 和 keywords 用于搜索、筛选和后续插件市场展示。
  "categories": ["Automation", "Security"],
  "keywords": ["clipboard", "redact", "privacy"],
  // engines 声明运行时兼容范围。
  "engines": {
    "clipcascade": ">=2.0.0",
    "wasi": "wasip1"
  },
  // activationEvents 只做能力声明，当前不自动注册规则。
  "activationEvents": [
    "onClipboard:text",
    "onCapability:content_redact"
  ],
  // repository 可选；私有插件可以删除这一块。
  "repository": {
    "type": "git",
    "url": "https://github.com/example/content-redact"
  },
  "author": {
    "name": "Example"
  },
  // contributes 预留插件贡献点，当前只展示和记录。
  "contributes": {
    "automation": {
      "capabilities": ["content_redact"],
      "rules": [],
      "commands": []
    }
  },
  // clipcascade 块是本项目专属字段，所有执行入口和资源限制都必须放在这里。
  "clipcascade": {
    "schemaVersion": 1,
    "enabled": true,
    // 第一阶段只支持 WASI，运行时按需加载。
    "entrypoint": { "type": "wasi", "path": "./content-redact.wasm" },
    "capabilities": ["content_redact"],
    // runtime 块控制单次插件调用的超时和输出上限。
    "runtime": { "timeout_seconds": 30, "max_output_bytes": 65536 }
  }
}
```

Notes:

- `README.md` is required and is used by the Web plugin page.
- Plugin IDs follow the VS Code style: `publisher.name`. Top-level `id` is no longer allowed in manifest files.
- `repository` is optional; private plugins do not need a repository URL.
- The `clipcascade` block stores platform-specific runtime fields. Top-level `entrypoint`, `capabilities`, `runtime`, and `enabled` are rejected.
- `plugins/clipcascade.plugin.schema.jsonc` provides a manifest JSONC Schema for editor validation.
- The first official plugin runtime supports WASI only, using stdin/stdout machine payloads.
- URL download, sensitive image detection, OCR, content redaction, and result relay are placeholders for now.
- Current official plugins include `url-platform-candidate` and `secret-detector`; `url-platform-candidate` currently prioritizes Douyin and Kuaishou platform URLs and can be expanded to more platforms later.
- Official plugin behavior, trigger rules, and execution boundaries are documented in [ClipCascade Official Plugins](_docs/official-plugins.md).
- Administrators can open `/plugins` from the dashboard footer.
- Build all official plugins with `./build-scripts/build.sh plugins`; output goes to `build/plugins/`.
- Release packages should place `clipcascade-server` and `plugins/` at the same level. The default `CC_AUTOMATION_PLUGINS_DIR=./plugins` is resolved relative to the server binary directory.

## Sync Strategy (Current Design)

### Text/Images

- Synchronized in real-time on copy.
- Screenshots, bitmap clipboard objects, and Android shared images are all treated as `image` and written back as image clipboard content, not as file paths.

### Files

- Single file and size `<= 8 MiB`: `file_eager` direct transfer.
- Single file and size `> 8 MiB and <= 25 MiB`: automatic HTTP relay; the receiver downloads it in the background and writes the local file path to the system clipboard.
- Single file and size `> 25 MiB and <= 2 GiB`: registered in the server deferred list only. It is not pushed into other devices' clipboards automatically; users can request/download it from the Web page.
- Multiple files or files over the supported limit: `file_stub` placeholder (metadata notification).

Android native receiving behavior:

- `file_eager`: decoded into app cache, with only the newest temporary copy retained; it is not written to the Android system clipboard, and the history save action writes it to `Download/`.
- `file_offer`: history stores the file name, size, and relay ID only. The content is downloaded to `Download/` only after the user taps it.
- Files above `25 MiB`: shown in the deferred files view and downloaded on demand, up to `2 GiB`.
- Images: previewed in the app and saved to `Download/` on demand.

Because Android share sources are exposed as `content://` URIs, the Android sending path continues to use HTTP relay for files up to `25 MiB`. This is a platform-specific source-handling path; it does not change the three-band protocol understood by PC receivers or pre-upload large files.

`file_eager` behavior on receiving end:

- Saved to temporary directory:
  - macOS/Linux: `${TMPDIR}/ClipCascade` or `/tmp/ClipCascade`
  - Windows: `%LOCALAPPDATA%\Temp\ClipCascade`
- Simultaneously writes file path to system clipboard, allowing direct paste.

Temp file cleanup:

- Automatically cleans up old temporary files older than `24 hours` when receiving files.
- Android eager-file cache keeps only the newest file. LAN discovery runs only while the main screen is in the foreground and restarts when the network/IP changes.

## Build and Environment

### Environment Preparation (Must read)

Base Environment:

- Go `1.22+` (Go `1.25` used in CI)
- Git
- `PATH` includes `$(go env GOPATH)/bin`

Supplementary requirements based on build target:

- Desktop/Linux cross-compilation: Requires Docker (ensure daemon is started).
- macOS Desktop build: Requires Xcode Command Line Tools (`xcode-select --install`).
- Android Native Persistence (`mobile-android-native`):
  - JDK `17`
  - Android SDK (`platform-tools`, `platforms;android-34`, `build-tools;34.0.0`)
  - Android NDK (Install via `brew install --cask android-ndk`)
  - `gomobile` (`go install golang.org/x/mobile/cmd/gomobile@latest && gomobile init`)

Suggested Environment Variables (Add to `~/.zshrc`):

```bash
export ANDROID_HOME="/opt/homebrew/share/android-commandlinetools"
export ANDROID_SDK_ROOT="$ANDROID_HOME"
export ANDROID_NDK_HOME="/opt/homebrew/Caskroom/android-ndk/<version>/AndroidNDK*.app/Contents/NDK"
```

Notes:

- Project includes a built-in `client-android-native-shell/android/gradlew`, no global Gradle installation required.
- If Gradle cache is corrupted, delete `.gradle-user-home/` in the root directory and retry.

### Linux Runtime Dependencies

Both the Linux desktop client and the graphical desktop UI rely on CGO and cannot be compiled as pure static binaries, so system libraries are required in addition to the binary itself. Install according to what you intend to run:

#### Headless Tray Client (`clipcascade-desktop-linux-amd64`)

```bash
# Debian / Ubuntu / UOS / deepin
sudo apt install libgtk-3-0 libayatana-appindicator3-1 libx11-6 xclip
```

| Dependency | Purpose | Effect if missing |
|------|------|----------|
| `libgtk-3-0` | GTK3 runtime for the tray UI | Fails to start |
| `libayatana-appindicator3-1` | Tray icon registration | Fails to start |
| `libx11-6` | Text/image clipboard access (`dlopen`ed at runtime, not link-time) | Text and image sync unavailable |
| `xclip` | Reading/writing the file clipboard (see the target note below) | Only file sync unavailable; text and images still work |

On Wayland sessions the file clipboard prefers `wl-copy` / `wl-paste`; installing the `wl-clipboard` package is recommended.

The file clipboard target depends on the desktop environment. GNOME-family file managers (including UOS / deepin `dde-file-manager`, which only honours its `kGnomeCopyKey` = `x-special/gnome-copied-files` constant) need `x-special/gnome-copied-files` with a `copy\n` prefix, while KDE (Dolphin) uses `text/uri-list`. Writing only `text/uri-list` on UOS/deepin makes the file manager aware of a clipboard change but unable to paste any file.

The tray links against `libayatana-appindicator3`, not the legacy `libappindicator3`. The two are ABI-compatible forks of the same project but have different sonames, so they are not interchangeable: UOS V20 only ships `libayatana-appindicator3.so.1`, which is why the build targets ayatana too (Debian 10 provides `libayatana-appindicator3-dev`).

#### Graphical Desktop UI (`clipcascade-ui-desktop-linux-amd64`)

```bash
sudo apt install libgl1 libx11-6 libxcursor1 libxrandr2 libxinerama1 libxi6 libxxf86vm1
```

Fyne requires OpenGL 2.0+. In virtual machines or environments without a GPU driver, install Mesa software rendering as a fallback:

```bash
sudo apt install libgl1-mesa-dri
```

This build does not need `libappindicator3` or webkit2gtk: Fyne's Linux tray uses the pure Go D-Bus implementation in `fyne.io/systray`, and Fyne v2.7 does not depend on webkit.

#### glibc Version Requirement

The CI `linux-amd64` artifacts are built inside a Debian 10 container and **require only glibc ≥ 2.28**, covering UOS V20, deepin 20, Debian 10+, Ubuntu 18.10 and newer.

If your target system is older than that (for example a CentOS 7 based distribution with glibc 2.17), the official artifacts cannot run and you need to compile on that system. `build-scripts/build_linux_legacy.sh` records the required system dependencies and build flags; adapt it to your distribution's package manager. Keeping its GLIBC guard (verifying the artifact's symbol version ceiling with `objdump -T`) is recommended.

### Common Build Commands

```bash
./build-scripts/build.sh server
./build-scripts/build.sh server-cross
./build-scripts/build.sh desktop
./build-scripts/build.sh cross
./build-scripts/build.sh desktop-ui
./build-scripts/build.sh desktop-ui-cross
./build-scripts/build.sh mobile-android-native
```

`mobile-android-native` output:

- `build/ClipCascade-Android-Installable.apk` (directly installable)

### Windows Console Mode

- Default: Windows desktop client keeps the console window (for logging).
- No-console tray mode:

```bash
CLIPCASCADE_WINDOWS_GUI=1 ./build-scripts/build.sh cross
```

## Environment Variables

### Server Runtime

Relative paths are resolved from the server binary directory. The recommended release layout is to place `database/`, `uploads/`, and `plugins/` beside `clipcascade-server`.

| Variable | Default | Description |
| --- | --- | --- |
| `CC_PORT` | `8080` | Server listen port |
| `CC_MAX_MESSAGE_SIZE_IN_MiB` | `20` | WebSocket single-message limit in MiB |
| `CC_MAX_MESSAGE_SIZE_IN_BYTES` | `0` | WebSocket single-message limit in bytes; takes priority when greater than 0 |
| `CC_P2P_ENABLED` | `false` | Enables P2P signaling and P2P sync mode |
| `CC_P2P_STUN_URL` | `stun:stun.l.google.com:19302` | WebRTC STUN server |
| `CC_ALLOWED_ORIGINS` | `*` | CORS allowed origins, comma-separated |
| `CC_SIGNUP_ENABLED` | `false` | Enables public signup |
| `CC_MAX_USER_ACCOUNTS` | `-1` | Maximum user count; `-1` means unlimited |
| `CC_ACCOUNT_PURGE_TIMEOUT_SECONDS` | `-1` | Account purge timeout; `-1` disables it |
| `CC_SESSION_TIMEOUT` | `525960` | Login session lifetime in minutes |
| `CC_MAX_UNIQUE_IP_ATTEMPTS` | `15` | Brute-force protection unique IP threshold |
| `CC_MAX_ATTEMPTS_PER_IP` | `30` | Brute-force protection per-IP threshold |
| `CC_LOCK_TIMEOUT_SECONDS` | `60` | Initial lock duration after protection triggers |
| `CC_LOCK_TIMEOUT_SCALING_FACTOR` | `2` | Lock duration multiplier after repeated triggers |
| `CC_BFA_CACHE_ENABLED` | `true` | Enables brute-force protection cache |
| `CC_DATABASE_PATH` | `./database/clipcascade.db` | SQLite database path |
| `CC_FILE_RELAY_ENABLED` | `true` | Enables HTTP file relay |
| `CC_FILE_RELAY_DIR` | `./uploads` | HTTP file relay storage directory |
| `CC_FILE_RELAY_MAX_BYTES` | `2147483648` | Maximum relay file size, 2 GiB by default |
| `CC_FILE_RELAY_TTL_SECONDS` | `86400` | Relay file retention time |
| `CC_EXTERNAL_BROKER_ENABLED` | `false` | Enables external STOMP broker |
| `CC_BROKER_HOST` | `localhost` | External broker host |
| `CC_BROKER_PORT` | `61613` | External broker port |
| `CC_AUTOMATION_ENABLED` | `false` | Enables server automation engine |
| `CC_AUTOMATION_MODE` | `observe` | Automation mode: `observe` only records, `enforce` can block |
| `CC_AUTOMATION_RULES_PATH` | empty | Automation rule JSONC file path |
| `CC_AUTOMATION_COMMANDS_PATH` | empty | Local command whitelist JSONC file path |
| `CC_AUTOMATION_PLUGINS_DIR` | `./plugins` | Plugin directory; relative paths resolve from the server binary directory |
| `CC_AUTOMATION_COMMAND_TIMEOUT_SECONDS` | `30` | Local command timeout per run |
| `CC_AUTOMATION_COMMAND_MAX_OUTPUT_BYTES` | `65536` | Maximum stdout bytes read from local commands |
| `CC_AUTOMATION_PLUGIN_TIMEOUT_SECONDS` | `30` | WASI plugin timeout per run |
| `CC_AUTOMATION_PLUGIN_MAX_OUTPUT_BYTES` | `65536` | Maximum stdout bytes read from WASI plugins |
| `CC_AUTOMATION_TRUSTED_COPY_ENABLED` | `false` | Enables `/api/automation/copy` trusted-copy endpoint |
| `CC_AUTOMATION_COPY_MAX_BYTES` | `262144` | Request body size limit for trusted-copy endpoint |

### Web Clipboard Runtime

| Variable | Default | Description |
| --- | --- | --- |
| `WEBCLIP_PORT` | `8090` | Web clipboard port; auto-increments when occupied |
| `WEBCLIP_DB_PATH` | binary dir `data.db` | Web clipboard database path |
| `WEBCLIP_UPLOAD_DIR` | binary dir `uploads/` | Web clipboard upload directory |

### Desktop Runtime

| Variable | Default | Description |
| --- | --- | --- |
| `CLIPCASCADE_PASSWORD` | empty | Desktop password override; skips reading password from local config |
| `XDG_CONFIG_HOME` | system default | Linux config directory standard variable |
| `APPDATA` | system default | Windows config directory standard variable |
| `WAYLAND_DISPLAY` | system default | Used on Linux to prefer Wayland clipboard tools when available |

### Build Scripts

| Variable | Default | Description |
| --- | --- | --- |
| `VERSION` | `git describe --tags --always` or `dev` | Injected binary version |
| `CLIPCASCADE_WINDOWS_GUI` | `0` | `1` hides the console window for Windows desktop builds |
| `ANDROID_HOME` | auto-detected Homebrew Android SDK | Android SDK path used by native Android builds |
| `ANDROID_SDK_ROOT` | follows `ANDROID_HOME` | Standard Android SDK path variable; filled from `ANDROID_HOME` when empty |
| `ANDROID_NDK_HOME` | auto-detected | Android NDK path used by native Android builds |
| `CC_ANDROID_API` | `26` | Android API level for native Android builds |
| `GRADLE_USER_HOME` | root `.gradle-user-home/` | Android Gradle cache directory; defaults to a repo-local directory |
| `JAVA_HOME` | system default | JDK path used by Gradle Wrapper; JDK 17 is recommended |
| `SDKROOT` | auto-detected Xcode SDK | macOS CGO SDK; invalid values are cleared by the build script |
| `PATH` | system default | Must contain build tools such as `go`, `gomobile`, `fyne`, `fyne-cross`, and `docker` |

## FAQ

### 1) Docker-related build failure

Error: `Cannot connect to the Docker daemon ...`

Solution:

```bash
open -a Docker
# or
open -a OrbStack
```

Confirm `docker info` works, then retry.

### 2) Login page shows "Signup is disabled" when clicking Create

This is the default configuration (signup closed).

- Enable public signup: `CC_SIGNUP_ENABLED=true`
- Recommended: Keep closed and have administrators add users via `/advance`.

### 3) File received notification but paste not working

Ensure both ends are using the latest desktop binaries and check logs for:

- `App: Preparing to send clipboard update Type=file_eager`
- `Clipboard: Received and wrote file to temporary directory`
- On Windows, also expect `Clipboard: Windows wrote file clipboard`. If this line is missing, the client did not successfully write `CF_HDROP`; update the Windows desktop binary.

### 4) Android shared image received on Windows but cannot paste

Ensure the Windows desktop binary includes image normalization on write. Android shared images are still synchronized as `image`; Windows should write image clipboard formats instead of falling back to file-path paste.

### 5) `GLIBC_2.xx not found` when starting on Linux

```
./clipcascade-desktop-linux-amd64: /lib/x86_64-linux-gnu/libc.so.6:
version `GLIBC_2.34' not found (required by ./clipcascade-desktop-linux-amd64)
```

The binary requires a newer glibc than your system provides. Artifacts before v2.2 were built on Ubuntu 24.04 and required `GLIBC_2.32` / `GLIBC_2.34`, which cannot start on Debian 10 based systems such as UOS V20 or deepin 20 (glibc 2.28).

Upgrade to a v2.2 or later artifact: Linux builds are now compiled inside a Debian 10 container and require only glibc ≥ 2.28. You can verify with:

```bash
objdump -T clipcascade-desktop-linux-amd64 | grep -oE 'GLIBC_2\.[0-9]+' | sort -uV
```

No version above `GLIBC_2.28` should appear in the output.

Also make sure the [Linux runtime dependencies](#linux-runtime-dependencies) are installed, especially `libgtk-3-0` and `libayatana-appindicator3-1` — missing those libraries also fails immediately at startup.

## 社区

[LINUX DO](https://linux.do/)


## License

This project is licensed under the [Apache License 2.0](LICENSE).
