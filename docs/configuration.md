# 配置与文件位置

## 配置文件

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

## 模型兼容策略覆盖

Router 内置了针对部分上游模型的兼容降级（见 [compatibility.md](compatibility.md)：强制工具选择降为 `auto`、带工具时移除 `response_format`、结构化输出时省略显式温度）。上游会持续修复这些限制，确认某个限制已解除后，可在配置文件里按模型关闭对应降级，无需等待新版本：

```json
{
  "model_policy_overrides": {
    "Tencent-kimi-k2.6": {
      "keep_tool_choice": true,
      "keep_response_format": false,
      "keep_temperature": false
    }
  }
}
```

| 字段 | 为 `true` 时的效果 |
| --- | --- |
| `keep_tool_choice` | 不再把 `tool_choice=required`/指定函数降级为 `auto` |
| `keep_response_format` | 不再在带工具时移除 `response_format` |
| `keep_temperature` | 不再在结构化输出时省略显式温度 |

键为模型 id（不区分大小写），改动后需重启服务生效。若上游尚未修复就打开覆盖，对应请求会收到上游的原始报错。

## 日志与 PID

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
