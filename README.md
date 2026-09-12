# ai-web-engine

一个面向 Android Root 的本地 AI Web 引擎：纯 Go 标准库、`CGO_ENABLED=0`、Web UI 通过 `go:embed` 内嵌，默认只监听 `127.0.0.1:6666`。

## 功能

- OpenAI 兼容协议和 Anthropic Messages SSE 流式响应；
- 模型列表、模型切换和推理等级切换；
- 新建/列出/删除/恢复历史会话；
- 会话使用 AES-256-GCM，`config/master.key` 与 `sessions/*.enc` 权限为 600；
- 启动时递归读取 `skills/**/SKILL.md`，并把 `shortx-rule-creator` 约束放在 system 消息首位；
- 配置页保存 provider、endpoint、protocol、API Key、默认模型和模型列表；
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
└── README.md
```
## 构建

```bash
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -ldflags "-s -w" \
  -o ai-web-engine-android-arm64 ./cmd
```


GitHub Actions 在 `v*` tag 上执行测试、`go vet`、ARM64 静态构建并创建 Release，资产名为 `ai-web-engine-android-arm64`。

## Android 部署

```sh
mkdir -p /data/local/ai-instruction/bin
cp ai-web-engine-android-arm64 /data/local/ai-instruction/bin/ai-web-engine
chmod 755 /data/local/ai-instruction/bin/ai-web-engine
sh scripts/init.sh
sh scripts/start.sh
```

脚本会从当前仓库 Raw 地址获取 `version.json`、Release 二进制和 `skills/shortx-rule-creator.zip`。初始化不会覆盖 `config/model_config.json`、`config/master.key` 或 `sessions/`。

## API

- `GET /`：嵌入式 Web UI
- `GET /health`
- `GET/POST /api/config`
- `GET /api/models`
- `GET/POST /api/sessions`
- `GET/DELETE /api/sessions/{id}`
- `POST /api/chat`：SSE
- `GET /api/skills`
- `POST /api/skills/reload`

## API Key：ShortX 环境变量优先

ShortX 启动指令会把环境变量 `%模型key%` 注入本地进程：

```sh
export AI_WEB_ENGINE_API_KEY="%模型key%"
```

Go 引擎优先读取 `AI_WEB_ENGINE_API_KEY`；`model_config.json` 只保存 `apiKeyEnv`，不会保存实际 Key。Web 设置页在该环境变量存在时也不会把输入 Key 写回配置文件。


可导入的三条云端脚本拉取指令位于 `shortx/`：

- `shortx/AI生成指令首次环境初始化.txt`
- `shortx/启动AI指令生成.txt`
- `shortx/结束AI指令生成.txt`

它们只拉取 `scripts/*.sh`，脚本更新无需重新导入。ShortX 仓库中的三段式 `.txt` 文件只负责下载/执行脚本，业务逻辑全部在云端脚本中。

## 停止安全边界

`scripts/stop.sh` 只读取 `/data/local/ai-instruction/bin/ai-web-engine.pid`，并核验 `/proc/<pid>/exe` 或命令行确实指向本引擎二进制后才发送信号。它不使用宽泛的 `pkill -f`，不会停止其他 Android/root 进程，也不会删除配置、master.key、会话、skills、日志或 PID 记录。


默认配置模板不包含真实 API Key。首次配置通过 Web UI 写入 `/data/local/ai-instruction/config/model_config.json`，该文件使用 600 权限；Android Keystore 不属于 Go 纯静态二进制本身的可调用标准库能力，因此本项目不伪称已经接入 Keystore。需要 Keystore 时，应由 Android 外部安全桥接程序提供，并把 Key 以环境变量注入服务。
