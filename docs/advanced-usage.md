# 高级用法

本页是常见使用场景的合集。参考类文档已拆分到下面几篇：

- [在 codex 中使用](./codex.md) —— 接入 codex、让模型进 `/model` 选择器
- [部署与构建](./deployment.md) —— 自行编译、Docker 镜像部署
- [命令参考](./cli-reference.md) —— 全部命令、参数与日志调试
- [配置与文件位置](./configuration.md) —— 配置文件、日志/PID 路径、环境变量
- [兼容性与已知限制](./compatibility.md) —— Responses / Messages 转换的注意事项

## 换一个端口启动

```bash
costrict-router start --addr 127.0.0.1:18080
```

Agent 工具中对应配置：

```text
Base URL: http://127.0.0.1:18080/v1
API Key: sk-costrict-...
```

## 重置本地 API Key

```bash
costrict-router key reset
costrict-router restart
```

如果旧 key 已遗失，只能重置生成新的 key；旧 key 会立即失效，正在运行的后台服务需要重启后才会加载新 key。

## 关闭本地鉴权

默认情况下，所有 `/v1/*` 接口都要带本地 API Key。如果你在一个**可信、仅本机可访问**的环境里嫌每次配 key 麻烦，可以关闭鉴权——关闭后请求不带 token 或带空 token 都能直接调用。

```bash
costrict-router auth disable     # 高危操作，会要求输入 yes 二次确认
costrict-router restart
```

> ⚠️ 关闭鉴权意味着**任何能访问监听地址的客户端，都能在无需 API Key 的情况下使用你的 CoStrict 账号**。请务必确认监听地址没有暴露到外部网络（如不要配合 `--addr 0.0.0.0:...` 使用）。

关闭后，每次 `serve` / `start` 都会打印一条安全警告，提醒鉴权处于关闭状态。如果此时监听地址**不是回环地址**（即可能被外部访问），警告会升级为更醒目的提示——`auth disable` 时也会基于你配置的监听地址给出同样的额外提醒。

重新开启：

```bash
costrict-router auth enable
costrict-router restart
```

说明：

- 关闭/开启只影响**本地鉴权**（客户端 → 本服务），不影响上游 CoStrict 的登录态。
- 改动需 `restart` 后生效（服务在启动时加载配置）。
- 脚本里可用 `costrict-router auth disable --yes` 跳过二次确认。
- 重新开启后若没有本地 API Key，会在下次 `start` 时自动生成。

## 手动指定兜底模型

当请求里的模型名在上游不存在时，costrict-router 会**自动把它替换成第一个可用模型**再转发，避免请求直接失败。这类未知模型名很常见，例如：

- codex 的 multi-agent、guardian 审查、记忆生成等辅助功能会用 `gpt-5.x` 之类的内部模型名；
- claude-code 会用 `claude-*` 模型名；
- 你在客户端里手填了一个上游没有的模型名。

所以默认情况下你**不需要做任何配置**，这些功能也能正常工作。

如果你不希望兜底到"第一个可用模型"，而是想指定具体替换成哪个模型，用 `fallback` 命令：

```bash
costrict-router fallback              # 交互式：列出可用模型，输入序号或模型名
costrict-router fallback glm-5        # 直接指定
costrict-router fallback --clear      # 清除自定义设置，恢复为「第一个可用模型」
```

说明：

- 优先级：你配置的兜底模型（必须当前确实可用）> 第一个可用模型。配置的模型暂时不可用时会自动退回第一个，避免替换成无效模型。
- 已存在于上游的模型名不受影响，原样透传。
- 模型列表尚未成功加载时不做替换、原样转发，保证 fail-safe。
- 改动需 `restart` 后生效（服务在启动时加载配置）。

## 使用独立配置文件

```bash
costrict-router login --base-url https://www.abc.com --config ./my-config.json
costrict-router start --config ./my-config.json
```

或使用环境变量：

```bash
export COSTRICT_ROUTER_CONFIG=./my-config.json
costrict-router status
```

## 查看服务是否正常

```bash
costrict-router status
curl http://127.0.0.1:14567/healthz
```

## 停止后台服务

```bash
costrict-router stop
```

## 开机自动启动服务

可以根据系统服务机制配置开机启动。例如向你的模型提问：

```text
我有一个编译后的二进制命令行可执行程序，启动命令是 ./costrict-router start，我要如何将其设定为开机自动启动？Windows 系统
```

根据实际情况替换末尾的系统名称即可。也可以直接叫 Agent 帮你处理。
