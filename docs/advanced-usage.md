# 高级用法

## 自行构建

```bash
git clone https://github.com/mokeyjay/costrict-router.git
cd costrict-router
go build -o costrict-router ./cmd/costrict-router
```

## Docker 镜像

容器默认使用 `/data/config.json` 保存登录态和本地 API Key。可以先创建一个 volume：

```bash
docker volume create costrict-router-data
```

### 1. 首次登录 CoStrict

```bash
docker run --rm -it \
  -v costrict-router-data:/data \
  ghcr.io/mokeyjay/costrict-router:latest \
  login --base-url https://www.abc.com
```

### 2. 生成本地 API Key：

```bash
docker run --rm -it \
  -v costrict-router-data:/data \
  ghcr.io/mokeyjay/costrict-router:latest \
  key reset
```

`key reset` 输出的 `sk-costrict-...` 只会显示一次，请立即保存。

### 3. 启动代理服务：

```bash
docker run -d --name costrict-router \
  -p 14567:14567 \
  -v costrict-router-data:/data \
  ghcr.io/mokeyjay/costrict-router:latest
```

之后就能愉快使用 `http://[宿主机 ip]:14567/v1` 啦～

## 命令一览

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
| `fallback` | 设置未知模型的兜底模型 | `costrict-router fallback` |
| `key reset` | 重置本地 API Key | `costrict-router key reset` |

## 命令参数

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
| `--debug` | 输出对话指标摘要 |
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

## 日志调试

滚动查看日志：

```bash
costrict-router logs
```

或前台调试：

```bash
costrict-router serve --debug
```

默认日志只记录程序流程和状态变化，例如启动、停止、刷新 token、上游错误摘要。`--debug` 会记录每次对话请求的指标摘要，例如模型、是否流式、状态码、首字节耗时、总耗时、请求/响应字节数和 token usage，不会打印完整 prompt。

如需排查真实请求内容，可以额外开启：

```bash
costrict-router serve --debug-full-request
```

`--debug-full-request` 会记录脱敏后的转发请求头和截断后的请求体，适合临时排查协议问题，不建议长期后台开启。

## 兼容性与已知限制

`/v1/responses` 和 `/v1/messages` 是在本地把请求翻译成上游能识别的格式再转发的。日常使用（多轮对话、图片、工具调用、流式输出）都没问题，但下面几种情况可能和官方 API 的体验不一致，使用前留个意：

- **提示词缓存不会生效**：上游不支持缓存，客户端带的缓存标记会被忽略。请求本身正常，只是每次都按完整内容计费、首字延迟也不会因缓存而变快。

- **靠"停止词"判断分支会失灵**：你可以正常用停止序列让模型在指定位置停下，但上游不会告诉我们究竟命中了哪个词，因此返回里的结束原因始终是"正常结束"。如果你的程序依赖"模型是因为停止词才停下"这个状态，会拿不到。

- **不能只靠 `previous_response_id` 续聊**：本工具不在本地保存历史对话，每次都要把完整上下文发上来。只传一个 `previous_response_id` 而不带完整内容的请求会被拒绝。

- **部分内置能力用不了**：联网搜索、文件检索、Computer Use 这类依赖上游专门支持的功能都不可用，只有你自己定义的函数工具能正常调用。请求里带了这些内置工具时它们会被静默忽略，不会报错。

- **图片要挑模型**：只有支持视觉的模型才能识别图片（用 `costrict-router models` 看 `IMAGE` 列）。给不支持的模型发图片可能被忽略或报错。

- **思考力度控制不精确**：模型自带思考过程并会原样透传，但你无法精确指定思考预算，相关参数只能粗略生效甚至被忽略。

## 文件位置

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

## 环境变量

| 环境变量 | 说明 | 示例 |
| --- | --- | --- |
| `COSTRICT_ROUTER_CONFIG` | 指定配置文件路径 | `COSTRICT_ROUTER_CONFIG=/tmp/cr.json costrict-router status` |
| `COSTRICT_ROUTER_LANG` | 指定语言，支持 `zh` / `en` | `COSTRICT_ROUTER_LANG=zh costrict-router status` |

## 常见用法

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

### 让 codex / claude-code 的辅助功能可用

codex、claude-code 的部分辅助功能会用上游不存在的模型名发请求（如 codex 的 multi-agent、claude-code 的 `claude-*`）而失效；执行 `costrict-router fallback` 从当前可用模型中选一个作为兜底，未知模型名就会被替换掉（改动需 `restart` 生效）。

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

可以根据系统服务机制配置开机启动。例如向你的模型提问：

```text
我有一个编译后的二进制命令行可执行程序，启动命令是 ./costrict-router start，我要如何将其设定为开机自动启动？Windows 系统
```

根据实际情况替换末尾的系统名称即可。也可以直接叫 Agent 帮你处理。
