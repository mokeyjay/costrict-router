<div align="center">

<img src="./logo.png" alt="CoStrict Logo" width="64">

# CoStrict Router

**将 CoStrict 私有化服务转成 OpenAI 兼容的本地接口**

_单文件 • 跨平台 • 自动续期_

</div>

---

`costrict-router` 是一个第三方的 [CoStrict](https://github.com/zgsm-ai/costrict) 接口转发工具，它可以将任意 OpenAI 兼容（Chat Completions）的请求转发到你指定的私有化 CoStrict 服务端上

你可以把它理解成一个 **本地 CoStrict 入口**：登录一次后，后续只需要把 Agent 工具的 Base URL 指向本地地址即可

## ✨ 特性

| 能力 | 说明 |
| --- | --- |
| 🔁 **OpenAI 兼容代理** | 本地暴露 `/v1/chat/completions` 和 `/v1/models` |
| 🔐 **登录态持久化** | 支持生成 CoStrict 登录链接，登录成功后持久化相应配置 |
| ♻️ **Token 自动刷新** | 根据 JWT 过期时间提前刷新，无感使用 |
| 🧭 **后台运行** | 支持 `start`、`stop`、`status`、`restart` |
| 📚 **浏览模型** | 查看当前账号可用模型、上下文窗口和图片能力 |

## 📦 安装
### 直接下载
你可以直接在 [**Release**](https://github.com/mokeyjay/costrict-router/releases/latest) 中下载编译后的可执行程序，解压后运行：

| 系统 | 架构 | 下载文件 |
| --- | --- | --- |
| macOS Apple Silicon | arm64 | `costrict-router_<version>_macos_arm64.tar.gz` |
| macOS Intel | amd64 | `costrict-router_<version>_macos_amd64.tar.gz` |
| Linux x86_64 | amd64 | `costrict-router_<version>_linux_amd64.tar.gz` |
| Linux ARM64 | arm64 | `costrict-router_<version>_linux_arm64.tar.gz` |
| Windows x86_64 | amd64 | `costrict-router_<version>_windows_amd64.zip` |
| Windows ARM64 | arm64 | `costrict-router_<version>_windows_arm64.zip` |

### 自行构建
```bash
go build -o costrict-router ./cmd/costrict-router
```

## 🚀 快速开始

### 1. 登录 CoStrict

把 `--base-url` 换成你的 CoStrict 私有化服务地址：

```bash
costrict-router login --base-url https://www.abc.com
```

命令会输出一个登录链接。复制到浏览器打开，完成登录后回到终端等待即可

> `login` 命令通常只需要执行一次，以后直接 `start` 启动服务即可  

### 2. 启动本地服务

```bash
costrict-router start
```

首次启动会生成本地 API Key。请立即保存终端中输出的 `sk-costrict-...`，它只会显示一次。

默认本地地址：

```text
http://127.0.0.1:14567/v1
```

### 3. 配置 Agent 工具

在支持 OpenAI 兼容接口的 Agent 工具里配置：

| 配置项 | 值 |
| --- | --- |
| Base URL | `http://127.0.0.1:14567/v1` |
| API Key | 初次启动时输出的 `sk-costrict-...` |
| Model | 使用 `costrict-router models` 查看 |

## 🧰 命令一览

| 命令 | 作用 | 示例 |
| --- | --- | --- |
| `login` | 登录并保存 CoStrict token | `costrict-router login --base-url https://www.abc.com` |
| `serve` | 前台启动本地代理 | `costrict-router serve --debug` |
| `start` | 后台启动本地代理 | `costrict-router start` |
| `stop` | 停止后台服务 | `costrict-router stop` |
| `status` | 查看后台服务状态 | `costrict-router status` |
| `restart` | 重启后台服务 | `costrict-router restart` |
| `logs` | 监看日志 | `costrict-router logs` |
| `models` | 查看可用模型 | `costrict-router models` |
| `key reset` | 重置本地 API Key | `costrict-router key reset` |

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

字段说明：

| 字段 | 含义 |
| --- | --- |
| `MODEL` | 模型 ID |
| `CONTEXT` | 上下文窗口 |
| `MAX_TOKENS` | 最大输出 token |
| `IMAGE` | 是否支持图片输入 |
| `COMPUTER_USE` | 是否支持 Computer Use |

## 📄 日志

滚动查看日志：

```bash
costrict-router logs
```

启用更详细的请求转发日志：

```bash
costrict-router start --debug
```

或前台调试：

```bash
costrict-router serve --debug
```

默认日志只记录程序流程和状态变化，例如启动、停止、刷新 token、上游错误摘要。`--debug` 会记录每次 `/v1/chat/completions` 的对话指标摘要，例如模型、是否流式、状态码、首字节耗时、总耗时、请求/响应字节数和 token usage，不会打印完整 prompt。

如需排查真实请求内容，可以额外开启：

```bash
costrict-router start --debug-full-request
```

`--debug-full-request` 会记录脱敏后的转发请求头和截断后的请求体，适合临时排查协议问题，不建议长期后台开启。

## ⚙️ 常用命令及参数

### `login`

| 参数 | 说明 |
| --- | --- |
| `--base-url` | CoStrict 服务端地址 |
| `--url` | CoStrict 插件生成的登录 URL |
| `--config` | 指定配置文件路径 |
| `--timeout` | 登录轮询超时时间，默认 `5m` |

### `serve`

| 参数 | 说明 |
| --- | --- |
| `--addr` | 本地监听地址，默认 `127.0.0.1:14567` |
| `--config` | 指定配置文件路径 |
| `--debug` | 输出 `/v1/chat/completions` 对话指标摘要 |
| `--debug-full-request` | 输出脱敏后的请求头和截断请求体 |
| `--log-file` | 指定日志文件 |

### `start`

| 参数 | 说明 |
| --- | --- |
| `--addr` | 本地监听地址 |
| `--config` | 指定配置文件路径 |
| `--debug` | 后台服务开启对话指标日志 |
| `--debug-full-request` | 后台服务输出脱敏后的请求头和截断请求体 |
| `--log-file` | 指定日志文件 |
| `--pid-file` | 指定 PID 文件 |

### `logs`

| 参数 | 说明 |
| --- | --- |
| `-n` | 开始跟随前先显示最近 N 行 |
| `--plain` | 去除 ANSI 高亮 |
| `--log-file` | 指定日志文件 |

### `key reset`

重置本地 API Key。新 key 只会在命令输出中显示一次，配置文件中只保存 hash。

| 参数 | 说明 |
| --- | --- |
| `--config` | 指定配置文件路径 |

## 📁 文件位置

### 配置文件

默认配置文件：

```text
<os.UserConfigDir>/costrict-router/config.json
```

常见系统路径：

| 系统 | 路径 |
| --- | --- |
| macOS | `~/Library/Application Support/costrict-router/config.json` |
| Linux | `~/.config/costrict-router/config.json` |
| Windows | `%AppData%\costrict-router\config.json` |

### 日志与 PID

默认后台文件：

```text
日志: <os.UserCacheDir>/costrict-router/costrict-router.log
PID:  <os.UserCacheDir>/costrict-router/costrict-router.pid
```

常见系统目录：

| 系统 | 目录 |
| --- | --- |
| macOS | `~/Library/Caches/costrict-router/` |
| Linux | `~/.cache/costrict-router/` |
| Windows | `%LocalAppData%\costrict-router\` |

## 🌐 环境变量

| 环境变量 | 说明 | 示例 |
| --- | --- | --- |
| `COSTRICT_ROUTER_CONFIG` | 指定配置文件路径 | `COSTRICT_ROUTER_CONFIG=/tmp/cr.json costrict-router status` |
| `COSTRICT_ROUTER_LANG` | 指定语言，支持 `zh` / `en` | `COSTRICT_ROUTER_LANG=zh costrict-router status` |

## 🔌 本地接口

当前本地代理暴露：

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| `POST` | `/v1/chat/completions` | OpenAI 兼容对话接口 |
| `GET` | `/v1/models` | OpenAI 兼容模型列表 |
| `GET` | `/healthz` | 本地服务健康检查 |

`/v1/*` 需要在请求头中携带本地 API Key：

```http
Authorization: Bearer sk-costrict-...
```

`/healthz` 当前保持公开，用于本地健康检查和 `status` 命令。

## ❓ 常见用法

### 换一个端口启动

```bash
costrict-router start --addr 127.0.0.1:18080
```

Agent 工具中对应配置：

```text
Base URL: http://127.0.0.1:18080/v1
API Key: sk-costrict-...
```

### 重置本地 API Key

```bash
costrict-router key reset
costrict-router restart
```

如果旧 key 已遗失，只能重置生成新的 key；旧 key 会立即失效，正在运行的后台服务需要重启后才会加载新 key。

### 使用独立配置文件

```bash
costrict-router login --base-url https://www.abc.com --config ./my-config.json
costrict-router start --config ./my-config.json
```

或使用环境变量：

```bash
export COSTRICT_ROUTER_CONFIG=./my-config.json
costrict-router status
```

### 查看服务是否正常

```bash
costrict-router status
curl http://127.0.0.1:14567/healthz
```

### 停止后台服务

```bash
costrict-router stop
```

### 开机自动启动服务

直接向你的模型提问 **我有一个编译后的二进制命令行可执行程序，启动命令是 `./costrict-router start`，我要如何将其设定为开机自动启动？ Windows 系统**  
根据实际情况替换末尾的系统名称即可。也可以直接叫 agent 帮你搞定
