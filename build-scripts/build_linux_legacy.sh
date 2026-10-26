#!/usr/bin/env bash
#
# 在 Debian 10 (buster, glibc 2.28) 环境内编译 Linux 桌面产物。
#
# 为什么需要这一步：
#   ubuntu-latest 现在是 Ubuntu 24.04（glibc 2.39），CGO_ENABLED=1 编出来的产物
#   硬绑 GLIBC_2.32 / GLIBC_2.34，在统信 UOS V20 / deepin 20 这类 Debian 10 底子
#   （glibc 2.28）的系统上会直接报 "version `GLIBC_2.34' not found"。
#   改用老 glibc 环境编译后，产物只要求 2.28；glibc 向后兼容，因此它在更新的
#   发行版上同样可用。
#
# 用法（由 CI 和 build-scripts/build.sh 在容器内调用）：
#   docker run --rm -v "$PWD:/src" debian:buster \
#     bash /src/build-scripts/build_linux_legacy.sh <desktop|desktop-ui|mobile-ui>
#
# 目标（对应仓库里三套不同的 Linux 桌面产物，别混用）：
#   desktop     client-desktop                 隐形式托盘端（GTK3 + appindicator）
#   desktop-ui  client-desktop/cmd/desktop-ui  Fyne 图形化面板（build.sh / 正式发布用的那套）
#   mobile-ui   client-mobile                  Fyne 客户端编成桌面版（CI 里 build-desktop-ui-native 那套）
#
# 环境变量：
#   VERSION               注入 main.Version 的版本号，默认 dev
#   GO_TOOLCHAIN_VERSION  Go 工具链版本，默认 1.25.0
#   OUT                   覆盖产物路径
set -euo pipefail

TARGET="${1:-desktop}"
VERSION="${VERSION:-dev}"
GO_TOOLCHAIN_VERSION="${GO_TOOLCHAIN_VERSION:-1.25.0}"

# Fyne 在 Linux 上的依赖：go-gl/glfw 会把 GLFW 的 C 源码一起编进去，
# 链接 -lGL -lX11 -lXrandr -lXxf86vm -lXi -lXcursor -lXinerama。
# 不需要 appindicator（fyne.io/systray 在 Linux 上是纯 Go 的 D-Bus 实现），
# 也不需要 webkit（Fyne v2.7 不依赖它）。
FYNE_DEPS="libgl1-mesa-dev xorg-dev libx11-dev libxcursor-dev libxrandr-dev libxinerama-dev libxi-dev libxxf86vm-dev"

case "$TARGET" in
    desktop)
        PKG_DIR=/src/client-desktop
        DEFAULT_OUT=/src/clipcascade-desktop-linux-amd64
        # getlantern/systray 需要 GTK3 + appindicator。这里必须用 ayatana，不能用
        # legacy libappindicator3：统信 UOS V20 只装了 libayatana-appindicator3.so.1，
        # 没有 libappindicator3.so.1，链老库的产物在目标机上会直接报找不到该 so。
        # buster main 里就有 libayatana-appindicator3-dev，所以 glibc 仍然是 2.28。
        # 剪贴板后端已内置 Wayland/X11 协议，不需要外部剪贴板工具或 libX11 开发头文件。
        TAGS=""
        DEPS="libgtk-3-dev libayatana-appindicator3-dev"
        VERSION_PKG=main
        ;;
    desktop-ui)
        PKG_DIR=/src/client-desktop/cmd/desktop-ui
        DEFAULT_OUT=/src/build/clipcascade-desktop-ui-linux-amd64
        # desktopui 标签会把 app/tray_default.go（唯一引用 desktop/ui 的文件）排除掉，
        # 所以这条产物不带 systray，也就不需要 GTK3 和 appindicator。
        TAGS=desktopui
        DEPS="$FYNE_DEPS"
        VERSION_PKG=main
        ;;
    mobile-ui)
        PKG_DIR=/src/client-mobile
        DEFAULT_OUT=/src/build/clipcascade-ui-desktop-linux-amd64
        TAGS=""
        DEPS="$FYNE_DEPS"
        # client-mobile 没有 main.Version 变量，不注入
        VERSION_PKG=""
        ;;
    *)
        echo "用法: $0 <desktop|desktop-ui|mobile-ui>" >&2
        exit 2
        ;;
esac

OUT="${OUT:-$DEFAULT_OUT}"

# ─── buster 已 EOL，官方源下线，切到 archive.debian.org ───
echo "deb http://archive.debian.org/debian buster main" > /etc/apt/sources.list
echo "deb http://archive.debian.org/debian-security buster/updates main" >> /etc/apt/sources.list
echo 'Acquire::Check-Valid-Until "false";' > /etc/apt/apt.conf.d/99archive

apt-get update
# shellcheck disable=SC2086
apt-get install -y --no-install-recommends \
    build-essential binutils pkg-config ca-certificates curl xz-utils $DEPS

# ─── Go 官方 linux 工具链是纯静态链接的（无 ld.so interp），可直接跑在 glibc 2.28 上 ───
curl -fsSL "https://go.dev/dl/go${GO_TOOLCHAIN_VERSION}.linux-amd64.tar.gz" | tar -C /usr/local -xz
export PATH="/usr/local/go/bin:$PATH"
export GOTOOLCHAIN=local
go version

# ─── 编译 ───
if [ -n "$VERSION_PKG" ]; then
    LDFLAGS="-s -w -X ${VERSION_PKG}.Version=${VERSION}"
else
    LDFLAGS="-s -w"
fi

mkdir -p "$(dirname "$OUT")"
cd "$PKG_DIR"
CGO_ENABLED=1 GOOS=linux GOARCH=amd64 \
    go build -tags "$TAGS" \
    -ldflags="$LDFLAGS" \
    -o "$OUT" .

# ─── 守卫：产物不允许要求高于 GLIBC_2.28 的符号版本 ───
echo "===== [$TARGET] 产物要求的 GLIBC 符号版本 ====="
vers="$(objdump -T "$OUT" | grep -oE 'GLIBC_2\.[0-9]+' | sort -uV || true)"
printf '%s\n' "$vers"

bad="$(printf '%s\n' "$vers" | awk -F'[.]' 'NF > 1 && $2 > 28' || true)"
if [ -n "$bad" ]; then
    echo "::error::[$TARGET] 产物要求了高于 2.28 的 GLIBC: $bad"
    exit 1
fi
echo "OK: [$TARGET] 最高只要求 GLIBC_2.28"

ls -lh "$OUT"
