# 在 Codex 中使用

> 💡 本文假设你已成功将服务启动在本地 `http://127.0.0.1:14567/v1` 

## 1. 配置 codex provider

编辑 `~/.codex/config.toml`，加入一个指向本地服务的 provider：

```toml
model_provider = "costrict-router"

[model_providers.costrict-router]
name = "CoStrict Router"
base_url = "http://127.0.0.1:14567/v1"
wire_api = "responses"
env_key = "COSTRICT_API_KEY"
```

`env_key` 指定存放本地 API Key 的环境变量名，你需要在启动 codex 前设置环境变量：

```bash
export COSTRICT_API_KEY=sk-costrict-...
```

或者將它配置到 `.zshrc` 之类的文件中，避免每次手动设置

## 2. 让模型出现在 `/model` 选择器（可选）

默认情况下，你在 codex 中只能看到 `gpt-*` 系列模型。如果你的 CoStrct 上游没有这个名字的模型，那本项目将会自动 fallback 到第一个可用的模型（也就是 `./costrict-router models` 命令返回的第一个模型）

你可以通过 `./costrict-router fallback` 命令[指定默认的兜底模型](./advanced-usage.md#手动指定兜底模型)；  
也可以通过 `./costrict-router codex-catalog` 命令来生成 `catalog` 文件，这样你在 codex 中就能方便地通过 `/model` 命令来切换模型

`codex-catalog` 会拉取当前账号可用模型（含真实上下文窗口、图片能力），写入 `~/.codex/costrict-router-model-catalog.json`，并把 `~/.codex/config.toml` 的 `model_catalog_json` 指向它（首次会备份为 `config.toml.bak`）。

## 3. 选择模型并使用（可选）

启动 codex，用 `/model` 选择，或直接在 `config.toml` 里固定：

```toml
model = "kimi-k2.5"
```

之后正常使用 codex 即可。

## 注意事项

- **目录是静态快照**：上游模型增减后，需要重新执行 `costrict-router codex-catalog` 刷新。
- **辅助功能自动兜底**：codex 的 multi-agent、guardian 等可能会用上游不存在的内部模型名（如 `gpt-5.x`），本项目会自动把未知模型名替换成第一个可用模型，无需额外配置。想指定替换成哪个模型见[手动指定兜底模型](./advanced-usage.md#手动指定兜底模型)。
- 仅生成文件、不想自动改 `config.toml` 时，加 `--no-config`
- codex 主目录非默认位置时用 `--codex-home`（或设 `CODEX_HOME`）。
