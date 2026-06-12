# 在 Claude Code 中使用

> 💡 本文假设你已成功将服务启动在本地 `http://127.0.0.1:14567/v1`

## 配置环境变量

Claude Code 通过环境变量指向本地服务。设置以下变量后启动 Claude Code：

```bash
export ANTHROPIC_BASE_URL=http://127.0.0.1:14567
export ANTHROPIC_AUTH_TOKEN=sk-costrict-...
# 让 /model 命令能够看到真实模型列表，而不是默认那些 sonnet opus
export CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY=1
```

建议把这些写进 `.zshrc` 之类的文件，避免每次手动设置
