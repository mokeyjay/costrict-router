# 高级用法

本页是常见使用场景的合集。参考类文档已拆分到下面几篇：

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

## 配合 codex 使用

使用 codex 时通常需要额外处理两件事。

**1. 让 CoStrict 模型出现在 `/model` 选择器**

codex 只有在 provider 使用 command 方式鉴权（或使用 codex 官方后端）时才会去拉取模型列表，普通 API Key 方式不会拉取，所以 codex 的 `/model` 选择器默认只显示它自带的 gpt 系列，看不到 CoStrict 模型。执行：

```bash
costrict-router codex-catalog
```

它会拉取当前账号可用模型，生成 `~/.codex/costrict-router-model-catalog.json`，并把 codex `~/.codex/config.toml` 顶层的 `model_catalog_json` 指向该文件（首次改动会保留一份 `config.toml.bak` 备份）。codex 配置该项后会改用本地静态目录、不再依赖网络拉取，`/model` 选择器即可看到这些模型。

几点注意：

- 这是**静态快照**：上游模型增减后需要重新执行本命令刷新。
- 配置后 codex `/model` **只会显示这些模型**，其自带的 gpt 系列会被替换掉。
- 改动在**下次启动 codex（或新开会话）** 时生效。
- 不想让命令自动改 `config.toml` 的话，加 `--no-config`，它只生成目录文件并打印出需要手动添加的配置行。
- codex 主目录不在默认位置时，用 `--codex-home` 指定（或设置 `CODEX_HOME` 环境变量）。

**2. 让 multi-agent 等辅助功能可用**

见下方[手动指定兜底模型](#手动指定兜底模型)。

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
