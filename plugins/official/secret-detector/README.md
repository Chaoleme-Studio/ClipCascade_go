# 密钥候选检测

检测剪贴板文本预览中的常见密钥、Token 和连接串候选。

当前插件只返回命中类型、风险等级和数量，不会输出原始密钥内容，不会阻断同步，不会上传远程服务，也不会修改剪贴板。

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
