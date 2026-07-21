# ClipCascade 插件

这个目录保存官方插件源码和第三方插件模板。

- `official/`: 内置官方插件源码。
- `_templates/`: 第三方插件起步模板。

运行时插件包通过下面的命令构建到 `build/plugins/`：

```bash
./build-scripts/build.sh plugins
```

WASI 插件通过 stdin/stdout 交换机器载荷，并由服务端运行时跨平台执行。

插件清单使用 `clipcascade.plugin.jsonc`，结构是 VS Code 风格元数据层加 ClipCascade 专属运行块：

- `publisher` 和 `name` 组成规范 ID: `publisher.name`。
- `displayName`、`description`、`version`、`license`、`categories`、`keywords`、`repository`、`author` 用于 Web 插件 registry 展示。
- `engines.clipcascade` 声明需要的 ClipCascade 运行时版本范围。
- `activationEvents` 和 `contributes` 预留给更完整的自动化集成。
- `clipcascade.entrypoint`、`clipcascade.capabilities`、`clipcascade.runtime` 定义 WASI 产物和资源限制。
- `clipcascade.plugin.schema.jsonc` 提供 JSONC Schema 校验。

当前官方插件：

- `url-platform-candidate`: 识别平台 URL 候选，当前优先支持抖音、快手，只返回平台和链接元数据。
- `secret-detector`: 检测常见密钥、Token、私钥和连接串候选，只返回类型统计，不回显原文。

旧版顶层 `id`、`entrypoint`、`capabilities`、`runtime`、`enabled` 不再接受。
