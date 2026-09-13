# ai-web-engine

一个面向 Android Root 的本地 AI Web 引擎：纯 Go 标准库、`CGO_ENABLED=0`、Web UI 通过 `go:embed` 内嵌，默认只监听 `127.0.0.1:6688`。

## 功能

- OpenAI Chat Completions、OpenAI Responses 和 Anthropic Messages SSE 流式响应；
- 模型列表、模型切换和推理等级切换；
- 新建/列出/删除/恢复历史会话；
- 会话使用 AES-256-GCM，`config/master.key` 与 `sessions/*.enc` 权限为 600；
- 启动时递归读取 `skills/**/SKILL.md`，并把 `shortx-rule-creator` 约束放在 system 消息首位；
- 配置页保存 provider、endpoint、protocol、API Key、默认模型和模型列表；协议支持 OpenAI Chat、OpenAI Responses、Anthropic；
- 移动端会话抽屉支持新建、恢复、删除历史会话；
- 只读设备能力接口提供 ABI、Root、SELinux、命令、路径和 DNS 能力，作为 ShortX 指令生成上下文；
- API Key 不出现在 GET 配置响应、日志和会话文件中；
- Android `/system/bin/sh` 兼容的云端 `init.sh`、`start.sh`、`stop.sh`，更新采用 `.new` 原子替换并保留用户数据。

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


发布流程会创建 `v1.1.5` Release，并提供以下独立资产：

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

`scripts/init.sh` 的 ZIP 校验兼容 Android 常见的 `unzip` 实现：优先使用 `unzip -Z1`，不支持时回退到标准 `unzip -l`，并额外运行 `unzip -t`、路径安全检查和解压后完整性检查。初始化会同时准备与当前 ABI 匹配的引擎、skills、`start.sh`、`stop.sh`、配置模板、`master.key` 和目录；所有资源先进入临时文件，全部通过后统一提交，失败则保留旧资源且不写完成标记。`scripts/start.sh` 固定使用浏览器允许的本机端口 `6688`。`6666` 会被 Chromium/Chrome 拒绝并显示 `ERR_UNSAFE_PORT`；启动时如果 PID 文件对应旧的 6666 引擎，会先按可执行文件精确校验并安全迁移。

## API

- `GET /`：嵌入式 Web UI
- `GET /health`
- `GET/POST /api/config`
- `GET /api/models`
- `GET/POST /api/sessions`
- `GET/DELETE /api/sessions/{id}`
- `POST /api/chat`：SSE
- `GET /api/device-capabilities`：只读 Android 设备技术能力
- `GET /api/skills`
- `POST /api/skills/reload`

## API Key：ShortX 环境变量优先

ShortX 启动指令会把环境变量 `%模型key%` 注入本地进程：

```sh
export AI_WEB_ENGINE_API_KEY="%模型key%"
```

Go 引擎优先读取 `AI_WEB_ENGINE_API_KEY`；`model_config.json` 只保存 `apiKeyEnv`，不会保存实际 Key。Web 设置页在该环境变量存在时也不会把输入 Key 写回配置文件。


ShortX 三条动作使用仓库索引生成器支持的 `type.googleapis.com/ShellCommand` 类型，并均以 `.new` 临时文件校验后替换云端脚本。

- `shortx/AI生成指令首次环境初始化.txt`
- `shortx/启动AI指令生成.txt`
- `shortx/结束AI指令生成.txt`

它们只拉取 `scripts/*.sh`，脚本更新无需重新导入。ShortX 仓库中的三段式 `.txt` 文件只负责下载/执行脚本，业务逻辑全部在云端脚本中。

## 停止安全边界

`scripts/stop.sh` 只读取 `/data/local/ai-instruction/bin/ai-web-engine.pid`，并核验 `/proc/<pid>/exe` 或命令行确实指向本引擎二进制后才发送信号。它不使用宽泛的 `pkill -f`，不会停止其他 Android/root 进程，也不会删除配置、master.key、会话、skills、日志或 PID 记录。


默认配置模板不包含真实 API Key。首次配置通过 Web UI 写入 `/data/local/ai-instruction/config/model_config.json`，该文件使用 600 权限；Android Keystore 不属于 Go 纯静态二进制本身的可调用标准库能力，因此本项目不伪称已经接入 Keystore。需要 Keystore 时，应由 Android 外部安全桥接程序提供，并把 Key 以环境变量注入服务。

## Android 网络与协议

模型请求由手机上的引擎进程直接发出。纯静态 Go 进程启动时会优先读取 Android `getprop net.dns1` 到 `net.dns4`，过滤失效的 `127.0.0.1`/`::1`，再通过 HTTPS 连接模型服务；不要求额外配置 DNS。`AI_WEB_ENGINE_DNS` 仅作为特殊设备的可选覆盖。

如果服务端使用 OpenAI Responses API，端点填写 `https://example.com/v1/responses` 并选择“OpenAI Responses”；引擎不会再拼接 `/v1/chat/completions`。填写 `https://cc-vibe.com/v1/responses` 时，请求路径就是 `/v1/responses`。
