<div align="center">

<img src="./logo.png" alt="CoStrict Logo" width="64">

# CoStrict Router

**将 CoStrict 私有化服务转换成 OpenAI / Anthropic 格式的本地接口**

_单文件 • 跨平台 • 自动续期 • 多格式兼容_

</div>

---

`costrict-router` 是一个第三方的 [CoStrict](https://github.com/zgsm-ai/costrict) 接口转发工具。它会在本地启动一个 OpenAI / Anthropic 兼容接口，将请求转发到你指定的 CoStrict 私有化服务端。

你可以把它理解成一个 **本地 CoStrict 入口**：登录一次后，后续只需要把 Agent 工具的 Base URL 指向本地地址即可。

## ✨ 特性

| 能力 | 说明 |
| --- | --- |
| 🔁 **多格式兼容** | 支持 Chat Completions（原生）、Responses（转换）、Messages（转换） |
| 🔐 **登录态持久化** | 生成 CoStrict 登录链接，登录成功后持久化 token 和本地配置 |
| ♻️ **Token 自动刷新** | 根据 JWT 过期时间提前刷新，无感使用 |
| 🧭 **后台运行** | 支持 `start`、`stop`、`status`、`restart` |
| 📚 **模型列表** | 查看当前账号可用模型、上下文窗口和图片能力 |

> CoStrict 原生仅支持 Chat Completions，其不支持的 Responses / Messages 格式由本工具在本地转换

## 📦 安装

### 本地运行

在 [Release](https://github.com/mokeyjay/costrict-router/releases/latest) 下载对应系统和架构的压缩包，解压即可

| 系统 | 架构 | 下载文件 |
| --- | --- | --- |
| macOS Apple Silicon | arm64 | `costrict-router_<version>_macos_arm64.tar.gz` |
| macOS Intel | amd64 | `costrict-router_<version>_macos_amd64.tar.gz` |
| Linux x86_64 | amd64 | `costrict-router_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | arm64 | `costrict-router_<version>_linux_arm64.tar.gz` |
| Windows x86_64 | amd64 | `costrict-router_<version>_windows_amd64.zip` |
| Windows ARM64 | arm64 | `costrict-router_<version>_windows_arm64.zip` |

### Docker 部署

见 [部署与构建](./docs/deployment.md)

## 🚀 快速开始

### 1. 登录 CoStrict

把 `--base-url` 换成你的 CoStrict 私有化服务地址：

```bash
costrict-router login --base-url https://www.abc.com
```

命令会输出一个登录链接。复制到浏览器打开，完成登录后回到终端等待即可。

> `login` 通常只需要执行一次，以后直接 `start` 启动服务即可。

### 2. 启动本地服务

```bash
costrict-router start
```

⚠️ 首次启动会生成本地 API Key，请保存终端中输出的 `sk-costrict-...`，它只会显示一次。


### 3. 配置 Agent 工具

| 配置项 | 值 |
| --- | --- |
| Base URL | `http://127.0.0.1:14567/v1`（默认） |
| API Key | 初次启动时输出的 `sk-costrict-...` |
| Model | 使用 `costrict-router models` 查看 |

> 💡 **使用 codex 的话**，需要额外配置 provider 并生成模型目录，详见 [在 codex 中使用](./docs/codex.md)。

> ⚠️ 如果你有特殊需要，本项目也支持 **[关闭本地鉴权](./docs/advanced-usage.md#关闭本地鉴权)**

## 🔌 本地接口

所有 `/v1/*` 接口都需要使用本地 API Key，两种鉴权头都支持（OpenAI 风格用 `Authorization`，Anthropic 风格用 `x-api-key`）：

```http
Authorization: Bearer sk-costrict-...
# 或
x-api-key: sk-costrict-...
```

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/chat/completions` | OpenAI Chat Completions 兼容接口，透明转发到 CoStrict 上游 |
| `POST` | `/v1/responses` | OpenAI Responses 兼容入口，会转换为 Chat Completions 转发到上游 |
| `POST` | `/v1/messages` | Anthropic Messages 兼容入口，会转换为 Chat Completions 转发到上游 |
| `GET` | `/v1/models` | 查看当前账号可用模型 |
| `GET` | `/healthz` | 本地服务健康检查 |

`/v1/responses` 与 `/v1/messages` 是本地转换出来的兼容入口，日常的对话、图片、工具调用、流式输出都能正常使用。少数高级特性受上游能力限制，详见[兼容性与已知限制](./docs/compatibility.md)。

## 📚 查看模型

```bash
costrict-router models
```

示例输出：

```text
MODEL        CONTEXT  MAX_TOKENS  IMAGE  COMPUTER_USE
glm-5        198000   32000       false  false
kimi-k2.5    256000   32000       true   false
```

字段含义：

| 字段 | 含义 |
| --- | --- |
| `MODEL` | 模型 ID |
| `CONTEXT` | 上下文窗口 |
| `MAX_TOKENS` | 最大输出 token |
| `IMAGE` | 是否支持图片输入 |
| `COMPUTER_USE` | 是否支持 Computer Use |

## 🧰 高级用法

- [在 codex 中使用](./docs/codex.md) —— 接入 codex、让模型进 `/model` 选择器
- [高级用法](./docs/advanced-usage.md) —— 常见使用场景（换端口、手动指定兜底模型等）
- [命令参考](./docs/cli-reference.md) —— 全部命令、参数与日志调试
- [配置与文件位置](./docs/configuration.md) —— 配置文件、日志/PID 路径、环境变量
- [部署与构建](./docs/deployment.md) —— 自行编译、Docker 镜像部署
- [兼容性与已知限制](./docs/compatibility.md) —— Responses / Messages 转换的注意事项
