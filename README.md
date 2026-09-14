# ai-web-engine

一个面向 Android Root 的本地 AI Web 引擎：纯 Go 标准库、`CGO_ENABLED=0`、Web UI 通过 `go:embed` 内嵌，默认只监听 `127.0.0.1:6688`。

## 功能

- OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages SSE 流式响应；
- 模型列表、模型切换和推理等级切换，支持 `off/low/medium/high/xhigh/max`，默认 `xhigh`；
- 新建/列出/删除/恢复历史会话；
- 会话使用 AES-256-GCM，`config/master.key` 与 `sessions/*.enc` 权限为 600；
- 启动时递归读取 `skills/**/SKILL.md`，并把 `shortx-rule-creator` 约束放在 system 消息首位；
- 可选读取 `/data/local/ai-instruction/This machine skills` 下的文件夹、`SKILL.md` 和 ZIP，启动/每次生成前重建索引，仅按需加载匹配扩展；
- 模型设置页支持多组 provider 的新增、编辑、切换、删除；每个 provider 使用独立的 `config/providers/<provider-id>.key` 文件（权限 600），协议支持 OpenAI Chat、OpenAI Responses、Anthropic；
- 移动端会话抽屉支持新建、恢复、删除历史会话；
- 只读设备能力接口提供 ABI、Root、SELinux、命令、路径和 DNS 能力，作为 ShortX 指令生成上下文；
- API Key 不出现在 GET 配置响应、日志和会话文件中；
- 支持富文本回复、Markdown 代码块、代码块内符号原样显示，以及通过本地官方格式校验后的快捷复制和一键导入 ShortX；
- ShortX 输出区分一键指令 `DirectAction` 与自动指令 `Rule`，仅允许官方 UTF-8 导入结构通过复制/导入；
- 项目采用专有许可证，默认保留全部权利，不允许未经书面授权的二次开发、复制、修改、衍生、分发、再许可、销售、托管或商用；
- Android `/system/bin/sh` 兼容的云端 `init.sh`、`start.sh`、`stop.sh`，更新采用 `.new` 原子替换并保留用户数据；
- `scripts/rollback.sh`：只允许从当前最新正式 Release 回退到上一个正式 Release，并按 ABI、ELF、SHA-256 和健康检查验证；
- `CHANGELOG.md`：源项目版本日志，独立日志站点会从正式 tag 自动生成；

## 目录结构

每个功能目录只保留一层职责入口，主程序入口为 `cmd/main.go`，不再使用 `cmd/server/main.go`：

```text
ai-web-engine/
├── cmd/                 # Go 程序入口
│   └── main.go
├── internal/            # 内部业务包
│   ├── api/
│   ├── config/
│   ├── crypto/
│   ├── model/
│   ├── session/
│   └── skills/
├── web/                 # 嵌入式前端资源与 embed 入口
├── scripts/             # Android 云端更新/启动/停止脚本
├── shortx/              # 三条 ShortX 云端脚本拉取指令
├── skills/              # shortx-rule-creator skill
├── .github/             # GitHub Actions
├── go.mod
├── version.json
├── manifest-android.json # Android 多架构资产清单
└── README.md
```
## 构建

```bash
# 单架构示例
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w" \
  -o ai-web-engine-android-arm64 ./cmd

# 完整四架构包由 GitHub Actions 自动构建并发布
```


发布流程会创建当前 tag 对应的 Release（当前为 `v1.2.2`），并提供以下独立资产：

- `ai-web-engine-android-arm64`：Android `arm64-v8a`，ELF64/AArch64；
- `ai-web-engine-android-armv7`：Android `armeabi-v7a`，ELF32/ARM EABI5；
- `ai-web-engine-android-x86_64`：Android `x86_64`，ELF64/x86-64；
- `ai-web-engine-android-x86`：Android `x86`，ELF32/i386；
- `ai-web-engine-android-all.zip`：包含以上四个二进制、`manifest-android.json` 和 `SHA256SUMS` 的全架构包。

初始化会读取 Android `ro.product.cpu.abi`，自动选择对应二进制，并同时校验 ELF class 和 machine；不会把全架构 ZIP 解压到手机运行目录。引擎支持 OpenAI Chat、OpenAI Responses、Anthropic SSE，并自动读取 Android 系统 DNS。

## Android 部署

```sh
mkdir -p /data/local/ai-instruction/bin
cp ai-web-engine-android-arm64 /data/local/ai-instruction/bin/ai-web-engine
chmod 755 /data/local/ai-instruction/bin/ai-web-engine
sh scripts/init.sh
sh scripts/start.sh
```

启动后会监听并打开 `http://127.0.0.1:6688`；不要再使用旧的 `6666`。

`scripts/init.sh` 的 ZIP 校验兼容 Android 常见的 `unzip` 实现：优先使用 `unzip -Z1`，不支持时回退到标准 `unzip -l`，并额外运行 `unzip -t`、路径安全检查和解压后完整性检查。初始化会同时准备与当前 ABI 匹配的引擎、skills、`start.sh`、`stop.sh`、配置模板、`config/providers/`、`master.key` 和目录；所有资源先进入临时文件，全部通过后统一提交，失败则保留旧资源且不写完成标记。`scripts/start.sh` 固定使用浏览器允许的本机端口 `6688`。`6666` 会被 Chromium/Chrome 拒绝并显示 `ERR_UNSAFE_PORT`；启动时如果 PID 文件对应旧的 6666 引擎，会先按可执行文件精确校验并安全迁移。

## API

- `GET /`：嵌入式 Web UI
- `GET /health`
- `GET/POST /api/config`
- `POST /api/config/reload`：从磁盘重新读取 provider 注册表和对应 Key
- `POST /api/engine/restart`：真实替换当前引擎进程
- `GET/POST /api/providers`、`DELETE /api/providers/{id}`
- `POST /api/providers/select`：切换全局活动 provider；会话仍可单独保存自己的 provider/model/reasoning 选择
- `GET /api/models`
- `GET/POST /api/sessions`
- `GET/PATCH/DELETE /api/sessions/{id}`
- `POST /api/generations`：创建不绑定浏览器连接的生成任务；
- `GET /api/generations?sessionId=...`：查询会话中的活动任务；
- `GET /api/generations/{taskId}/events`：重放/订阅 reasoning、delta、done、error 事件；
- `GET /api/shortx/index-sources`：返回官方说明页与 snow Raw 索引地址；
- `POST /api/chat`：兼容旧客户端的直接 SSE；
- `POST /api/shortx/validate`：校验并规范化 DirectAction/Rule 官方导入文本
- `GET /api/device-capabilities`：只读 Android 设备技术能力
- `GET /api/skills`
- `POST /api/skills/reload`

## Provider Key：本地独立文件

多 provider 配置写入 `config/model_config.json` 的非敏感注册表；每个 provider 的真实 Key 写入对应的 `config/providers/<provider-id>.key`，文件权限为 `600`。网页不会回显 Key，留空编辑 Key 会保留原文件；删除 provider 会同步删除对应 Key 文件。

会话持久化 `providerId`、`modelId` 和 `reasoningLevel`。请求会严格按会话 provider 读取对应 Key，不按名称猜测，也不会把其他 provider 的 Key 作为 fallback。

ShortX 官方指令分为两类：一键指令 `DirectAction` 使用尾部 `{"type":"da"}`；自动指令 `Rule` 使用尾部 `{"type":"rule"}`。两者都必须是主体 JSON、单独一行 `###------###`、尾部类型 JSON 的 UTF-8 文件。页面只从代码块内部提取载荷，复制/导入前会去除 Markdown 围栏并重新规范化；Android 支持 Web Share 时会以 `.txt` 文件打开系统分享面板供 ShortX 导入，不支持时会同时复制并下载规范文件。

旧单 provider 配置仍兼容 `model.key` 和 `AI_WEB_ENGINE_API_KEY`；ShortX 启动动作保留环境变量注入，主要用于旧配置迁移和兼容场景，不会覆盖多 provider 的本地 Key 文件。


ShortX 三条动作使用仓库索引生成器支持的 `type.googleapis.com/ShellCommand` 类型，并均以 `.new` 临时文件校验后替换云端脚本。

- `shortx/AI生成指令首次环境初始化.txt`
- `shortx/启动AI指令生成.txt`
- `shortx/结束AI指令生成.txt`

它们只拉取 `scripts/*.sh`，脚本更新无需重新导入。ShortX 仓库中的三段式 `.txt` 文件只负责下载/执行脚本，业务逻辑全部在云端脚本中。

## 停止安全边界

`scripts/stop.sh` 只读取 `/data/local/ai-instruction/bin/ai-web-engine.pid`，并核验 `/proc/<pid>/exe` 或命令行确实指向本引擎二进制后才发送信号。它不使用宽泛的 `pkill -f`，不会停止其他 Android/root 进程，也不会删除配置、master.key、会话、skills、日志或 PID 记录。


默认配置模板不包含真实 API Key。首次配置通过 Web UI 写入 `/data/local/ai-instruction/config/model_config.json`，provider Key 写入 `/data/local/ai-instruction/config/providers/<provider-id>.key`，配置和 Key 文件均使用 600 权限；Android Keystore 不属于 Go 纯静态二进制本身的可调用标准库能力，因此本项目不伪称已经接入 Keystore。需要 Keystore 时，应由 Android 外部安全桥接程序提供，并把 Key 以环境变量注入服务。

## Android 网络与协议

模型请求由手机上的引擎进程直接发出。纯静态 Go 进程启动时会优先读取 Android `getprop net.dns1` 到 `net.dns4`，过滤失效的 `127.0.0.1`/`::1`，再通过 HTTPS 连接模型服务；不要求额外配置 DNS。`AI_WEB_ENGINE_DNS` 仅作为特殊设备的可选覆盖。

如果服务端使用 OpenAI Responses API，端点填写 `https://example.com/v1/responses` 并选择“OpenAI Responses”；引擎不会再拼接 `/v1/chat/completions`。填写 `https://cc-vibe.com/v1/responses` 时，请求路径就是 `/v1/responses`。

## 许可证 / License

本仓库**不是开源软件**。当前及未来版本采用 [`LICENSE`](./LICENSE) 中的 **PROPRIETARY LICENSE — ALL RIGHTS RESERVED（专有许可证，保留所有权利）**。

除通过 GitHub 查看公开仓库所必需的有限平台权限外，未经 `snowzlmbot` 事先书面授权，不得使用、复制、下载、修改、移植、二次开发、创建衍生作品、发布、分发、再分发、再许可、销售、托管、作为服务提供或进行商业利用。仓库公开、Fork、下载、提交 Issue 或 Pull Request 均不代表获得额外许可。

第三方依赖和材料仍适用其各自许可证。此前已经依据 MIT 许可证合法取得的历史版本，其既有授权通常不能通过本次变更追溯撤销；本专有许可证适用于采用该许可证发布的当前及未来版本。具体法律适用和可执行性请咨询所在地律师。
