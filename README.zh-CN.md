# Motok.ai CLI

独立的 Go 命令行项目，直接调用官方 API 或自定义兼容服务，不依赖 Motok.ai 后端、数据库或 Node.js。

[English](README.md)

## 构建与安装

开发需要 Go 1.26.1 或更新版本，在本目录执行：

```sh
go build -trimpath -o bin/motok ./cmd/motok
./bin/motok help --lang zh
```

发布后，用户可以通过脚本安装编译好的二进制，无需安装 Go：

```sh
MOTOK_RELEASE_BASE_URL=https://your-release-host/v0.1.0 sh install.sh --version v0.1.0
```

发布地址需要指向你实际托管的目录，其中包含对应平台的压缩包和 `SHA256SUMS`。当前项目尚未配置或发布公网下载地址。安装脚本通过 HTTPS 下载、验证 SHA-256，无需 sudo，默认安装到用户本地 bin 目录。可用 `--bin-dir` 或 `MOTOK_BIN_DIR` 更改安装位置，再将该目录加入 PATH。

构建全部平台的发布包并从本地安装：

```sh
sh scripts/build-release.sh v0.1.0
sh install.sh --from-dir ./dist --version v0.1.0
motok --version
```

发布包覆盖 macOS、Linux、Windows 的 AMD64 和 ARM64 架构。Windows 用户解压对应 ZIP，核对 SHA-256 后将 `motok.exe` 所在目录加入 PATH。发布构建需要 `tar`、`zip` 和 `sha256sum` 或 `shasum`；远程安装需要 `curl`。校验和用于发现损坏或不匹配的文件，发布地址本身仍需可信。

## 渠道与能力路由

密钥和地址只从配置文件、环境变量读取，不需要 profile，也不支持 `--profile`、`--api-key`、`--base-url`。

```sh
motok config init
motok config show
```

默认使用支持注释的 YAML，同时兼容 `.json` 配置，另有 [JSON 示例](config.example.json)。未知字段、重复 YAML 键和多文档会被拒绝；省略 `version` 时默认为 1。

配置自动按 `config.yaml`、`config.yml`、`config.json` 顺序查找，保留已有 JSON 配置的兼容性。配置文件默认位于系统用户配置目录下的 `motok/config.yaml`，生成权限为 0600，已有文件不会覆盖。可以通过环境变量指定文件：

```sh
export MOTOK_CONFIG=./motok.config.yaml
motok config init
```

`channels` 定义各渠道的协议、地址和密钥；`routes` 指定 text、image、video 分别使用哪个渠道及模型。完整示例见 [config.example.yaml](config.example.yaml)：

```yaml
version: 1
language: zh

# Channel names are arbitrary; provider selects the request protocol.
channels:
  claude:
    provider: anthropic
    base_url: https://api.anthropic.com/v1
    api_key_env: ANTHROPIC_API_KEY
  openai:
    provider: openai
    base_url: https://api.openai.com/v1
    api_key_env: OPENAI_API_KEY
    max_tokens_field: max_completion_tokens
  media:
    provider: openai
    base_url: http://127.0.0.1:8092/v1
    api_key_env: MOTOK_API_KEY
    base_url_env: MOTOK_BASE_URL

# routes.channel references a channel name.
routes:
  text:
    channel: claude
    model: your-text-model
  image:
    channel: openai
    model: your-image-model
  video:
    channel: media
    model: your-video-model
```

将示例模型名替换为账号实际可用的模型 ID。使用时不需要切换配置：

```sh
motok text generate --prompt "你好"
motok image generate --prompt "水彩风格的小猫" -o cat.png
motok video generate --prompt "小猫走过花园" --wait -o cat.mp4
```

文本自动走 `routes.text`，图片生成和编辑走 `routes.image`，视频生成、查询与下载走 `routes.video`。同一渠道可以用于多种能力，每种能力配置不同的模型。`channels.<名称>.provider` 表示协议（openai / anthropic / gemini，省略时默认 openai）；`routes.<能力>.channel` 则引用自定义渠道名。例如渠道 `company-proxy` 的协议可以是 anthropic，而 `routes.text.channel` 填 company-proxy。`response generate` 使用 text 路由，要求该渠道支持 OpenAI Responses。

模型查询、登录检查和原生请求默认使用 text 路由，可以用 `--type` 指定能力：

```sh
motok models --type image --json
motok auth status --type video
motok request POST /embeddings --type text --data request.json --json
```

`models` 查询对应渠道提供的模型列表，不会推断模型的能力。生成命令不允许使用与命令本身冲突的 `--type`。

密钥可以在渠道中直接填写 `api_key`，或用 `api_key_env` 指向环境变量。配置字符串不会展开 shell 变量，`.env` 文件不会自动加载。`config show` 会隐藏所有渠道的密钥。

优先级：

1. 渠道与协议：按 `routes` 找到相应的 `channels` 配置。
2. 密钥：`api_key_env` 指向的非空环境变量 → 渠道中的 `api_key`。
3. 地址：`base_url_env` 指向的非空环境变量 → 渠道的 `base_url` → 协议默认地址。
4. 模型：`--model` → `MOTOK_TEXT_MODEL` / `MOTOK_IMAGE_MODEL` / `MOTOK_VIDEO_MODEL` → 路由的 `model` → 渠道的 `model`。
5. 语言：`--lang` → `MOTOK_LANG` → 配置中的 `language` → 英文。

未填写环境变量引用时，按协议使用 `OPENAI_API_KEY` / `OPENAI_BASE_URL`、`ANTHROPIC_API_KEY` / `ANTHROPIC_BASE_URL` 或 `GEMINI_API_KEY` / `GEMINI_BASE_URL`。相同协议的多个渠道，可以分别指定自己的变量名。`MOTOK_API_KEY` 和 `MOTOK_BASE_URL` 仅作用于显式引用它们的渠道，不会覆盖其他渠道。

没有配置文件时，默认通过 OpenAI 官方环境变量访问 OpenAI，三种能力共用该默认渠道。多渠道请先运行 `config init`。旧 profile 格式不再接受，需要手动改为 channels/routes；CLI 不会自动覆盖旧配置或丢弃密钥。

自定义 URL 会保留路径前缀，没有路径时，OpenAI/Anthropic 补 `/v1`，Gemini 补 `/v1beta`。支持自建 HTTP 服务，网络传输密钥时应使用 HTTPS。API 请求不跟随重定向。

渠道的 `max_tokens_field` 可选择 `max_completion_tokens`（默认）或 `max_tokens`；Anthropic 和 Gemini 使用原生字段。`--max-tokens` 默认 1024。

## 使用

```sh
motok auth status
motok login
motok models --json
motok text generate --prompt "你好"
motok text generate --model your-model --prompt "你好"
motok response generate --prompt "你好" --json
motok image generate --prompt "水彩风格的小猫" -o cat.png
motok image edit --prompt "背景改为蓝色" --file cat.png -o edited.png
motok video generate --model your-video-model --prompt "小猫走过花园" --wait -o cat.mp4
motok video status TASK_ID
motok video download TASK_ID -o cat.mp4
motok help --lang zh
motok help --ai
```

`login` 使用已配置的密钥访问模型列表验证连接，不会打开浏览器、创建用户或另存会话。如果上游不提供模型列表接口，`login` 和 `models` 可能失败，但仍可直接调用生成接口。`auth status` 只检查本地配置，`status` 则进行在线密钥验证。

| 能力 | OpenAI 兼容协议 | Anthropic | Gemini |
| --- | --- | --- | --- |
| 文本、模型列表 | 支持 | 支持 | 支持 |
| Responses | 上游支持时可用 | 不支持 | 不支持 |
| 图片生成与编辑 | 上游模型支持时可用 | 不支持 | 支持图片输出的 generateContent 模型 |
| 视频任务与下载 | 实现 `/videos` 协议的兼容服务 | 不支持 | 不支持 |
| 原生 JSON 请求 | 支持 | 支持 | 支持 |

视频命令适用于 Motok.ai 已提供的兼容协议，不转换 Ark、Veo 等原生视频任务格式。OpenAI 官方 Videos API 已于 2026 年 9 月 24 日关闭，视频辅助命令会拒绝其官方域名；应配置实际支持视频的兼容服务。详见 [官方说明](https://developers.openai.com/api/reference/resources/videos/methods/create)。实际模型与参数始终取决于所选服务。

## 脚本和 Agent

```sh
motok image generate --prompt "水彩小猫" --dry-run --json
motok text generate --prompt - --json
motok request POST /embeddings --data request.json --json
motok request POST /messages --type text --data message.json --json
motok request POST /audio/speech --data speech.json -o speech.mp3
```

`request` 接收相对于 API 前缀的路径、原生 JSON 请求体，使用所选协议的鉴权头，可用于辅助命令尚未覆盖的接口与参数，不会转换厂商原生协议。`--data -`、`--prompt -` 从标准输入读取。

- `--json`：成功结果写入 stdout；错误 JSON 写入 stderr，退出码为 1。
- `--dry-run`：不联网、不要求密钥，仅展示请求计划，隐藏鉴权头和参考图字节。
- `--output` / `-o`：写入新文件，不覆盖已有文件；与 `--json` 同用时输出文件元数据。
- JSON 请求输入上限为 4 MiB，辅助命令的 JSON 响应上限为 32 MiB，二进制文件流式下载。
- 图片多结果响应默认保存第一张；完整结果用不带 `-o` 的 `--json` 获取。
- 文本辅助命令采用非流式请求；额外协议选项可通过原生请求提供。
- 视频默认每 10 秒查询一次，整体超时 10 分钟，可用 `--poll-interval`、`--timeout` 修改。
- 视频任务编号在等待前输出到 stderr；超时不会取消远程生成任务。
- CLI 不自动重试生成 POST，避免重复创建计费任务。

## 验证

```sh
go test -race ./...
go vet ./...
go build -o bin/motok ./cmd/motok
sh -n install.sh scripts/build-release.sh
```

测试使用本地模拟 HTTP 服务和测试密钥，不访问官方付费接口。本 Go 模块不依赖 `../services`；YAML 解析库为纯 Go 实现，随二进制一起打包。
