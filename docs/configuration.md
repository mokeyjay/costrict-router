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

Router 内置了针对部分上游模型的兼容降级（见 [compatibility.md](compatibility.md)：强制工具选择降为 `auto`、带工具时移除 `response_format`、结构化输出时省略显式温度）。上游会持续修复这些限制，确认某个限制已解除后，可在配置文件里按模型关闭对应降级，无需等待新版本。同一处配置还可以按模型关闭上游内置思考（`disable_thinking`）：

```json
{
  "model_policy_overrides": {
    "Tencent-kimi-k2.6": {
      "keep_tool_choice": true,
      "keep_response_format": false,
      "keep_temperature": false
    },
    "Ark-glm-5.2": {
      "disable_thinking": true
    }
  }
}
```

| 字段 | 为 `true` 时的效果 |
| --- | --- |
| `keep_tool_choice` | 不再把 `tool_choice=required`/指定函数降级为 `auto` |
| `keep_response_format` | 不再在带工具时移除 `response_format` |
| `keep_temperature` | 不再在结构化输出时省略显式温度 |
| `disable_thinking` | 强制注入 `thinking: {"type":"disabled"}` 关闭该模型的内置思考，会覆盖请求里自带的 `thinking` 字段 |

键为模型 id（不区分大小写），改动后需重启服务生效。若上游尚未修复就打开覆盖，对应请求会收到上游的原始报错。

服务运行时只在启动时读取一次配置，并会在刷新 token 时把内存中的配置整体写回文件，运行中手动编辑的内容可能被覆盖。建议先 `costrict-router stop`，改完再 `costrict-router start`。

### 关闭思考（`disable_thinking`）

视觉转写、OCR、简单分类这类子任务不需要深度推理，关闭思考后输出 token 和耗时通常会大幅下降。该开关按模型全局生效，不能按单次请求控制：配置后所有客户端调用这个模型都不再思考，如只想对子任务关闭，可给子任务单独指定一个模型。

各模型实测结果（2026-10-22，原生 Chat、Messages、Responses、流式与多轮工具调用均已覆盖）：

| 模型 | 结果 |
| --- | --- |
| `Baidu-glm-5.1`、`Baidu-glm-5.2`、`Ark-glm-5.2` | ✅ 生效，思考消失，工具调用正常 |
| `Tencent-kimi-k2.6` | ✅ 生效，含视觉输入；多轮工具调用正常 |
| `Tencent-deepseek-v4-pro` | ✅ 生效，工具调用正常 |
| `Tencent-glm-5.1` | ❌ 不要配置：上游拒绝 `disabled`，所有请求都会返回 400 |
| `Baidu-glm-5.3` | ❌ 不要配置：思考并未真正关闭，而是混进正文输出，并残留一个 `</think>` |

上游可能随时调整，未列出的模型请自行验证后再使用。

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
