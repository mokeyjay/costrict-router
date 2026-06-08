# 命令参考

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
| `codex-catalog` | 生成 codex 模型目录，让 CoStrict 模型出现在 codex `/model` 选择器 | `costrict-router codex-catalog` |
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

### `codex-catalog`

拉取上游可用模型，生成 codex 的模型目录文件 `~/.codex/costrict-router-model-catalog.json`，并把 codex 的 `config.toml` 顶层 `model_catalog_json` 指向它。用法详见 [高级用法 - 配合 codex 使用](./advanced-usage.md#配合-codex-使用)。

| 参数 | 说明 |
| --- | --- |
| `--config` | 指定 costrict-router 配置文件路径 |
| `--codex-home` | codex 主目录，默认 `$CODEX_HOME` 或 `~/.codex` |
| `--no-config` | 只生成目录文件，不改写 codex `config.toml`（会打印需手动添加的配置行） |

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
