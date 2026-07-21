# ClipCascade v2.0 - 服务端自动化与插件框架

## 发布说明

v2.0 引入服务端自动化引擎和插件框架，为剪贴板内容识别、审计、候选任务和后续外部程序调用预留统一扩展能力。

本版本的重点是框架能力，不是内置重型下载、OCR 或模型能力。默认自动化关闭，不影响现有同步链路。

## 核心更新

### [feature] 服务端自动化引擎

- 新增服务端剪贴板事件归一化模型。
- 新增规则匹配、审计记录和自动化任务表。
- 支持 `audit`、`enqueue_task`、`run_plugin`、`run_command`、`notify_sender`、`block` 等动作。
- 默认规则不包含 `block`，不会拦截现有剪贴板同步。
- `block` 只有在显式配置规则且 `CC_AUTOMATION_MODE=enforce` 时才会真正阻断。

### [feature] WASI 插件框架

- 新增 `plugins/` 官方插件目录和第三方插件模板。
- 插件清单统一使用 `clipcascade.plugin.jsonc`。
- 清单格式参考 VS Code extension manifest，并通过 `clipcascade` 块声明 ClipCascade 专属执行字段。
- 插件 ID 使用 `publisher.name`，不再接受旧式顶层 `id`。
- WASI 插件按任务启动，不是常驻进程，降低常驻内存占用。
- 插件通过 stdin/stdout 交换机器载荷。

### [feature] 官方插件

#### secret-detector

- 插件 ID：`clipcascade.secret-detector`
- 能力：`secret_detect`
- 检测剪贴板文本预览中的常见密钥、Token、私钥和连接串候选。
- 返回命中类型、风险等级和数量。
- 不回显原始密钥，不阻断同步，不修改剪贴板。
- 自动化任务持久化前会对常见密钥候选做脱敏副本，避免任务表保存原始密钥。

#### url-platform-candidate

- 插件 ID：`clipcascade.url-platform-candidate`
- 能力：`url_download_candidate`
- 识别平台 URL 候选。
- 当前优先支持抖音、快手平台链接。
- 只记录平台和 URL 候选元数据，不下载内容，不调用远程服务，不修改剪贴板。

### [feature] Web 插件页面

- 管理员页面新增 `/plugins`。
- 首页底部增加插件页入口。
- 插件页展示插件数量、启用数量、异常数量和能力数量。
- 展示插件名称、简介、README 摘要、仓库链接、能力标签和状态。
- 支持插件启停覆盖值，覆盖值写入数据库，不直接修改 manifest。
- 自动化总开关关闭时，插件页仍可扫描和展示服务端二进制同级 `plugins/` 目录。

### [feature] 官方插件构建

- `./build-scripts/build.sh plugins` 构建全部官方 WASI 插件。
- `./build-scripts/build.sh all` 已包含官方插件构建。
- 构建产物输出到 `build/plugins/`。
- 插件发布布局固定为服务端二进制同级 `plugins/` 目录。

推荐布局：

```text
release/
  clipcascade-server
  plugins/
    secret-detector/
      clipcascade.plugin.jsonc
      README.md
      secret-detector.wasm
    url-platform-candidate/
      clipcascade.plugin.jsonc
      README.md
      url-platform-candidate.wasm
```

## 配置更新

新增服务端自动化相关环境变量：

- `CC_AUTOMATION_ENABLED`
- `CC_AUTOMATION_MODE`
- `CC_AUTOMATION_RULES_PATH`
- `CC_AUTOMATION_COMMANDS_PATH`
- `CC_AUTOMATION_PLUGINS_DIR`
- `CC_AUTOMATION_COMMAND_TIMEOUT_SECONDS`
- `CC_AUTOMATION_COMMAND_MAX_OUTPUT_BYTES`
- `CC_AUTOMATION_PLUGIN_TIMEOUT_SECONDS`
- `CC_AUTOMATION_PLUGIN_MAX_OUTPUT_BYTES`
- `CC_AUTOMATION_TRUSTED_COPY_ENABLED`
- `CC_AUTOMATION_COPY_MAX_BYTES`

路径类配置默认按服务端二进制所在目录解析。

## 安全边界

- 自动化默认关闭。
- E2EE 开启时，服务端仍无法读取明文正文，只能处理 envelope 元数据。
- 当前官方插件不会拦截、不会回填剪贴板、不会上传远程服务。
- 本地命令执行必须通过命令白名单配置，不允许插件绕过白名单直接调用系统命令。
- 插件入口路径必须位于插件目录内部。

## 文档更新

- 新增官方插件说明文档：`_docs/official-plugins.md`。
- 更新 README / README_en 的插件说明和环境变量表。
- 更新 V2.0 middleware / automation PRD。

## 影响范围

- **Server**：新增自动化引擎、规则、审计、任务、插件加载和插件管理 API。
- **Web Admin**：新增插件 registry 页面。
- **Build Scripts**：新增官方 WASI 插件构建流程。
- **Plugins**：新增官方插件和第三方插件模板。
- **Desktop / Android**：不执行服务端插件；客户端同步链路默认不受影响。

## 兼容说明

- 默认关闭自动化时，现有剪贴板同步行为保持不变。
- 插件 manifest 使用 JSONC，不保留旧 JSON manifest 兼容。
- 旧式顶层 `id`、`schema_version`、`enabled`、`entrypoint`、`capabilities`、`runtime` 字段会被拒绝。

---

*记录人：Codex*
