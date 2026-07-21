# ClipCascade 官方插件说明

本文档说明当前仓库内置的官方插件、触发方式和运行边界。

## 基本原则

- 插件只在服务端自动化引擎中生效，客户端没有插件执行逻辑。
- 自动化总开关默认关闭：`CC_AUTOMATION_ENABLED=false`。
- 插件页可以在自动化关闭时展示插件目录；只有开启自动化后，规则才会触发插件执行。
- 当前官方插件不做拦截、不改写剪贴板、不上传远程服务，只记录审计和任务结果。
- WASI 插件按任务启动，不是常驻进程；执行完成后释放。

## 运行布局

服务端运行时只看服务端二进制同级的 `plugins/` 目录：

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

构建全部官方插件：

```bash
./build-scripts/build.sh plugins
```

`./build-scripts/build.sh all` 已包含官方插件构建。

## 官方插件列表

### secret-detector

插件 ID：`clipcascade.secret-detector`

能力：`secret_detect`

作用：

- 检测剪贴板文本预览中的常见密钥、Token、私钥和连接串候选。
- 返回命中类型、风险等级和数量。
- 不回显原始密钥内容。
- 不阻断同步，不修改剪贴板。

当前识别范围：

- GitHub Token。
- OpenAI API Key。
- AWS Access Key ID。
- Google API Key。
- Stripe Secret Key。
- Slack Token。
- JWT。
- 私钥头。
- 带密码的数据库连接串。
- Bearer Token。

默认规则：

- 规则 ID：`secret-candidate`
- 只匹配 `clipboard.type == text`。
- 只在 `clipboard.text_preview` 命中密钥候选正则时触发。
- 动作：`audit`、`enqueue_task`、`run_plugin`。
- 不包含 `block` 动作。

持久化安全：

- 插件执行时会读取服务端事件里的文本预览做检测。
- 写入 `AutomationTask.input_json` 前，服务端会对常见密钥候选做脱敏副本。
- 脱敏只影响任务表记录，不影响原始剪贴板同步，也不影响插件检测。

### url-platform-candidate

插件 ID：`clipcascade.url-platform-candidate`

能力：`url_download_candidate`

作用：

- 检测剪贴板文本中的平台 URL 候选。
- 当前优先识别抖音、快手，后续可以继续扩展其他平台。
- 返回平台和 URL 候选元数据。
- 不下载视频，不调用外部服务，不修改剪贴板。

默认规则：

- 规则 ID：`url-platform-candidate`
- 只匹配 `clipboard.type == text`。
- 只在文本预览里出现当前支持的平台域名时触发。
- 动作：`audit`、`enqueue_task`、`run_plugin`。
- 不包含 `block` 动作。

## 是否影响原有同步流程

默认情况下不影响，因为自动化总开关关闭。

开启 `CC_AUTOMATION_ENABLED=true` 后：

- 规则匹配在服务端收到剪贴板事件后执行。
- 默认官方规则不包含 `block`，所以不会拦截广播。
- WASI 插件通过 goroutine 异步执行，不等待插件完成后再同步。
- 当前会在命中规则时写审计和任务记录，因此开启自动化后会有少量数据库写入开销。

如果后续要做真正阻断，必须显式配置 `block` 动作，并把 `CC_AUTOMATION_MODE` 设置为 `enforce`。当前官方默认规则没有这么做。
