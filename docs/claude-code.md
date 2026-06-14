# 在 Claude Code 中使用

> 💡 本文假设你已成功将服务启动在本地 `http://127.0.0.1:14567/v1`

## 配置环境变量

Claude Code 通过环境变量指向本地服务。设置以下变量后启动 Claude Code 即可：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:14567
export ANTHROPIC_AUTH_TOKEN=sk-costrict-...
# 让 /model 命令能够看到真实模型列表，而不是默认那些 sonnet opus
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1

claude
```

> 💡 把上述 `export` 行写进 `.zshrc` 之类的文件，就能避免每次手动设置

## 不想影响到 Anthropic 官方服务或使用中转站时

创建独立的配置目录和启动命令（例如 `claude-local`）可以使 Claude Code 指向本项目的同时不影响官方/中转站服务  
或者如果你之前使用过 CC-Switch 之类的软件，你可能会发现部分环境变量不生效（它们可能会将连接配置写入到 `~/.claude/settings.json`，它的优先级**高于**命令行和环境变量，会把你新设的 `ANTHROPIC_BASE_URL` 之类的配置覆盖掉）

可以这样解决：

生成一个新的配置目录指向本项目

```bash
mkdir -p ~/.claude-local
cat > ~/.claude-local/settings.json <<'JSON'
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:14567",
    "ANTHROPIC_AUTH_TOKEN": "sk-costrict-...",
    "CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1"
  }
}
JSON
alias claude-local='CLAUDE_CONFIG_DIR=~/.claude-local claude'
```

之后用 `claude-local` 启动即可指向本地，原有配置原封不动。
