# Go WASI 插件模板

使用这个模板可以构建跨平台 ClipCascade WASI 插件。

```bash
GOOS=wasip1 GOARCH=wasm go build -trimpath -ldflags="-s -w" -o plugin.wasm ./cmd/plugin
```

插件通过 stdin 读取服务端传入的机器载荷，并通过 stdout 写回一个机器可解析结果。
