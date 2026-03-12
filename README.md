# goclaudeclaw ⚡

A Go rewrite of [ClaudeClaw](https://github.com/lustan3216/claudeclaw) — a daemon that bridges Telegram bots to the Claude Code CLI, with shared memory, cron scheduling, and multi-bot support.

## Why Go

The original TypeScript/Bun implementation worked well, but Go gives us:
- Single static binary, zero runtime dependencies
- Goroutine-per-bot model with clean lifecycle management
- Built-in race detector catches concurrency bugs at dev time
- Lower memory footprint for a long-running daemon

## Features

- **Multi-bot** — run multiple Telegram bots simultaneously, each in its own goroutine, all sharing the same claude workspace and memory
- **Auto subagent detection** — classifies incoming messages as `FOREGROUND` (interactive) or `BACKGROUND` (long-running, fire-and-forget) using a lightweight claude call
- **Session persistence** — stores session IDs per workspace in `.goclaudeclaw_session` so `--resume` survives restarts
- **Debounce** — configurable window to merge rapid-fire messages before sending to claude
- **Heartbeat** — periodic prompts on a timer, with quiet windows (e.g. no pings at 3am) and timezone support
- **Cron jobs** — standard cron expressions (with second precision) for scheduled prompts
- **Config hot-reload** — YAML config reloads automatically via fsnotify, no restart needed
- **claude-mem integration** — shared memory REST client (search + add) across all bots
- **Security levels** — `locked / strict / moderate / unrestricted` maps to claude permission flags

## Project Structure

```
goclaudeclaw/
├── cmd/goclaudeclaw/main.go      # CLI entry (cobra), wires everything together
├── internal/
│   ├── bot/
│   │   ├── telegram.go           # Long-polling goroutine per bot, reconnect on drop
│   │   └── dispatcher.go         # Debounce, auth, classify, route to runner
│   ├── runner/
│   │   ├── runner.go             # Serial queue per workspace, executes claude CLI
│   │   └── classifier.go         # One-shot claude call to classify BACKGROUND/FOREGROUND
│   ├── session/
│   │   └── session.go            # .goclaudeclaw_session file per workspace
│   ├── memory/
│   │   └── claudemem.go          # claude-mem REST client
│   ├── scheduler/
│   │   ├── heartbeat.go          # Ticker + quiet window logic
│   │   └── cron.go               # robfig/cron wrapper with hot-reload support
│   ├── config/
│   │   └── config.go             # Viper + fsnotify config manager
│   └── daemon/
│       └── daemon.go             # PID file, signal handling, logger setup
├── config.example.yaml
├── go.mod
└── Makefile
```

## Quick Start

```bash
# 1. Clone
git clone https://github.com/lustan3216/goclaudeclaw
cd goclaudeclaw

# 2. Create config
make config          # copies config.example.yaml → config.yaml
$EDITOR config.yaml  # fill in your bot tokens and allowed_users

# 3. Build
make build           # outputs ./dist/goclaudeclaw

# 4. Run
./dist/goclaudeclaw --config config.yaml

# Or install to $GOPATH/bin
make install
goclaudeclaw --config config.yaml
```

## Configuration

See [`config.example.yaml`](config.example.yaml) for the full reference. Key sections:

```yaml
workspace: /path/to/project   # Where claude runs

bots:
  - name: "main"
    token: "BOT_TOKEN"
    allowed_users: [123456789]  # REQUIRED — prevents public access
    debounce_ms: 1500

security:
  level: moderate               # locked | strict | moderate | unrestricted

heartbeat:
  enabled: true
  interval_minutes: 15
  quiet_windows:
    - start: "23:00"
      end: "08:00"
  timezone: "Asia/Shanghai"
```

**Security levels:**

| Level | Behavior |
|-------|----------|
| `locked` | Read-only — system prompt constrains claude (TODO) |
| `strict` | Confirm every tool call (claude default) |
| `moderate` | Most ops auto-approved (default) |
| `unrestricted` | `--dangerously-skip-permissions` |

## Bot Commands

| Command | Description |
|---------|-------------|
| `/start`, `/help` | Show help |
| `/clear` | Clear current session (start fresh) |
| `/status` | Show bot name, workspace, security level |
| `/bg <task>` | Force background mode for a task |

## How Message Classification Works

When a message arrives after the debounce window:

1. A separate one-shot `claude -p "<classification prompt>"` call is made
2. Claude replies `BACKGROUND` or `FOREGROUND`
3. **FOREGROUND** → run normally, stream output back to Telegram
4. **BACKGROUND** → reply immediately with "processing in background", run in a separate goroutine, notify when done

The classifier times out after 10 seconds and defaults to FOREGROUND on any error.

## Session Management

Each workspace has a `.goclaudeclaw_session` file containing the Claude session ID. This enables `--resume <id>` on every call so conversation context persists across:
- Bot restarts
- Multiple bots (they share the same session file)
- Config reloads

Clear a session with `/clear` or by deleting the file.

## Makefile Targets

```
build         Compile for local platform → ./dist/
build-linux   Cross-compile for Linux amd64
build-all     All platforms (linux, darwin arm64/amd64)
install       Install to $GOPATH/bin
run           go run with --debug
test          go test -race ./...
lint          golangci-lint run
fmt           gofmt -w .
tidy          go mod tidy
clean         Remove ./dist/ and build cache
config        Create config.yaml from example (non-destructive)
```

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/spf13/cobra` | CLI commands and flags |
| `github.com/spf13/viper` | Config loading + hot-reload |
| `github.com/go-telegram-bot-api/telegram-bot-api/v5` | Telegram long-polling |
| `github.com/robfig/cron/v3` | Cron scheduling with second precision |
| `github.com/fsnotify/fsnotify` | File system watching (used by viper) |

## Development

```bash
# Run with race detector
go run -race ./cmd/goclaudeclaw --config config.yaml --debug

# Validate config without starting
goclaudeclaw validate --config config.yaml

# Run tests
make test

# Check for issues
make vet lint
```

## Related

- [claudeclaw](https://github.com/lustan3216/claudeclaw) — original TypeScript/Bun implementation
- [claude-mem](https://github.com/anthropics/claude-mem) — shared memory MCP server
- [Claude Code CLI](https://docs.anthropic.com/claude-code) — the underlying AI engine
