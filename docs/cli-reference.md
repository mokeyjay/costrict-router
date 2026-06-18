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
| `test` | 真实发一条消息验证能否连通后端大模型 | `costrict-router test` |
| `fallback` | 设置未知模型的兜底模型 | `costrict-router fallback` |
| `codex-catalog` | 生成 codex 模型目录，让 CoStrict 模型出现在 codex `/model` 选择器 | `costrict-router codex-catalog` |
| `key reset` | 重置本地 API Key | `costrict-router key reset` |
| `auth disable` | 关闭本地鉴权（需二次确认） | `costrict-router auth disable` |
| `auth enable` | 重新开启本地鉴权 | `costrict-router auth enable` |

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

### `test`

刷新 token 后向上游真实发起一次非流式对话请求，验证当前实例能否连通后端大模型。可用 `--model` 指定模型名；不指定时按「兜底模型 → 第一个可用模型」自动选择。旧的位置参数写法仍然兼容。成功会打印模型回复，失败会打印上游返回的错误，便于区分配置/登录问题与上游模型不可用。

```bash
costrict-router test                  # 自动选模型，发送「你好」
costrict-router test --model kimi-k2.5 # 指定模型
costrict-router test --model glm-5 --prompt "ping"
costrict-router test --prompt "ping"  # 自定义测试消息
costrict-router test all              # 所有模型各测 3 次并按速度排名
costrict-router test all --runs 5     # 所有模型各测 5 次
```

| 参数 | 说明 |
| --- | --- |
| `--model` | 要测试的模型名称；省略则用兜底模型或第一个可用模型 |
| `[model]` | 兼容旧用法的位置参数，不可与 `--model` 同时使用 |
| `all` / `--all` | 串行测试全部可用模型并输出速度排名 |
| `--runs` | 全部模型模式下每个模型的测试次数，默认 `3`，范围 `1-100` |
| `--prompt` | 发送的测试消息；单模型默认 `你好`，全部模型模式默认 `只回复 OK` |
| `--config` | 指定配置文件路径 |
| `--timeout` | 请求超时时间，默认 `1m` |

### `codex-catalog`

拉取上游可用模型，生成 codex 的模型目录文件 `~/.codex/costrict-router-model-catalog.json`，并把 codex 的 `config.toml` 顶层 `model_catalog_json` 指向它。用法详见 [在 codex 中使用](./codex.md)。

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

### `auth disable` / `auth enable`

开关本地鉴权。`disable` 是高危操作，需二次确认。详见 [高级用法 - 关闭本地鉴权](./advanced-usage.md#关闭本地鉴权)。

| 参数 | 说明 |
| --- | --- |
| `--config` | 指定配置文件路径 |
| `--yes` | 仅 `disable`：跳过二次确认（用于脚本） |

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
