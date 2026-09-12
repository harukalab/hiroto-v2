# Hiroto

<p align="center">
  <img src="assets/hiroto.svg" alt="Hiroto" width="720">
</p>

> **Your terminal, your agent.** Write code, hunt bugs, automate workflows — all from the command line.

[![Go](https://img.shields.io/badge/Go-1.26+-00ADD8?logo=go)](https://go.dev)
[![Version](https://img.shields.io/badge/version-v0.6.2-blue)](https://github.com/harukalab/hiroto-v2/releases)
[![License](https://img.shields.io/badge/license-MIT-green)](LICENSE)
[![TUI](https://img.shields.io/badge/TUI-terminal-8FBF7F)](https://github.com/harukalab/hiroto-v2)

---

## Quick start

```bash
# One-liner install
curl -fsSL https://raw.githubusercontent.com/harukalab/hiroto-v2/main/install.sh | bash

# Or clone and build
git clone https://github.com/harukalab/hiroto-v2.git
cd hiroto-v2
make install
```

**Config** lives in `~/.hiroto/config.yaml`. That's it.

---

## Usage

| Command | What it does |
|---------|-------------|
| `hiroto` | Interactive TUI |
| `hiroto -q "who am i?"` | One-shot answer |
| `hiroto -c "prompt"` | Continue last session (one-shot) |
| `hiroto --resume <id>` | Reopen saved session in TUI |
| `hiroto --api` | OpenAI-compatible server |

### TUI shortcuts

| Key | Action |
|-----|--------|
| `Enter` | Send |
| `Ctrl+P` | Switch model |
| `Ctrl+R` | Resume session |
| `Ctrl+S` | Save |
| `Ctrl+L` | Clear |
| `Ctrl+C` | Quit |
| `↑↓` | History |

### Slash commands

Type `/` to open the picker. **31 commands** covering session control, model config, coding, iteration, tools, and productivity.

> `/todo`, `/memory`, `/bg`, `/goal`, `/steer`, `/compress`, `/rollback` — the full list in the picker.

---

## What it does

- **Terminal** — shell commands, file read/write/patch, background processes, LSP diagnostics
- **Browser** — headless Chrome: navigate, click, type, JS exec, screenshot
- **Code** — execute Python scripts that call back into Hiroto's tools
- **Safety** — auto git checkpoint before writes, `/rollback` to undo
- **Search** — web search, page extraction, vision (image analysis)
- **Automation** — sub-agents, cron, standing goals
- **API server** — `hiroto --api` exposes OpenAI-compatible endpoints at `localhost:20129`

---

## Configuration

```yaml
# ~/.hiroto/config.yaml
model:
  provider: openai
  name: your-model
  base_url: "https://your-api.com/v1"
  api_key: ""
  timeout: 60s
  max_tokens: 4096

api:
  port: 20129
```

No env files needed — keys go directly in config.

---

## Project structure

```
cmd/hiroto/          TUI, one-shot, API server
internal/
  agent/             Agent loop, system prompt, compression
  api/               OpenAI-compatible HTTP server
  llm/               OpenAI client (streaming)
  tools/             Tools + registry + LSP + safety
  skills/            Skill discovery
  memory/            Persistent memory
  session/           Conversation persistence
  config/            YAML config
skills/              240+ bundled skills
```

---

## License

MIT — [LICENSE](LICENSE)

Built from scratch in Go. No Python runtime for the core.
