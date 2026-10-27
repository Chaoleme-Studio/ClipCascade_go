# ClipCascade Go - Build & Cross-Compile
# 真正的构建逻辑统一在 build-scripts/build.sh，本文件仅作便捷入口。
# Usage: make server | make desktop | make desktop-ui | make android | make all | make tidy

.PHONY: all server server-cross plugins desktop desktop-ui desktop-ui-cross \
        web-clip web-clip-cross android ios cross docker tidy test clean help

# Versioning
VERSION ?= 2.8
BUILD_SCRIPT := ./build-scripts/build.sh

all:
	VERSION=$(VERSION) $(BUILD_SCRIPT) all

server:
	VERSION=$(VERSION) $(BUILD_SCRIPT) server

server-cross:
	VERSION=$(VERSION) $(BUILD_SCRIPT) server-cross

plugins:
	VERSION=$(VERSION) $(BUILD_SCRIPT) plugins

desktop:
	VERSION=$(VERSION) $(BUILD_SCRIPT) desktop

desktop-ui:
	VERSION=$(VERSION) $(BUILD_SCRIPT) desktop-ui

desktop-ui-cross:
	VERSION=$(VERSION) $(BUILD_SCRIPT) desktop-ui-cross

web-clip:
	VERSION=$(VERSION) $(BUILD_SCRIPT) web-clip

web-clip-cross:
	VERSION=$(VERSION) $(BUILD_SCRIPT) web-clip-cross

android:
	VERSION=$(VERSION) $(BUILD_SCRIPT) mobile-android-native

ios:
	VERSION=$(VERSION) $(BUILD_SCRIPT) mobile-ios

cross:
	VERSION=$(VERSION) $(BUILD_SCRIPT) cross

docker:
	VERSION=$(VERSION) $(BUILD_SCRIPT) docker

tidy:
	$(BUILD_SCRIPT) tidy

test:
	go test ./core/... ./server/...
	cd client-desktop && go test ./...
	cd server-web-clip && go build ./...

clean:
	$(BUILD_SCRIPT) clean

help:
	@$(BUILD_SCRIPT)
