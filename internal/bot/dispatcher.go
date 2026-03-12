// Package bot 实现 Telegram 消息路由、防抖和前台/后台任务分发。
package bot

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/lustan3216/goclaudeclaw/internal/config"
	"github.com/lustan3216/goclaudeclaw/internal/runner"
)

// incomingMsg 是防抖窗口内收集的原始消息。
type incomingMsg struct {
	text     string
	from     string
	chatID   int64
	receivedAt time.Time
}

// debounceState 跟踪每个 chat 的防抖状态。
type debounceState struct {
	timer    *time.Timer
	messages []incomingMsg
	mu       sync.Mutex
}

// Dispatcher 负责消息路由和防抖聚合。
// 每个 bot 实例共享同一个 Dispatcher，通过 chatID 区分会话。
type Dispatcher struct {
	mu       sync.Mutex
	debounce map[int64]*debounceState // chatID → 防抖状态

	runnerMgr  *runner.Manager
	classifier *runner.Classifier
	cfg        *config.Config
	botCfg     config.BotConfig
	botAPI     *tgbotapi.BotAPI
	workspace  string
}

// NewDispatcher 创建消息分发器。
func NewDispatcher(
	botAPI *tgbotapi.BotAPI,
	botCfg config.BotConfig,
	cfg *config.Config,
	runnerMgr *runner.Manager,
	workspace string,
) *Dispatcher {
	return &Dispatcher{
		debounce:   make(map[int64]*debounceState),
		runnerMgr:  runnerMgr,
		classifier: runner.NewClassifier("claude"),
		cfg:        cfg,
		botCfg:     botCfg,
		botAPI:     botAPI,
		workspace:  workspace,
	}
}

// UpdateConfig 热重载时更新配置（调用方应在配置变更回调中调用）。
func (d *Dispatcher) UpdateConfig(cfg *config.Config, botCfg config.BotConfig) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.cfg = cfg
	d.botCfg = botCfg
}

// Handle 接收来自 Telegram 的单条消息，进入防抖队列。
func (d *Dispatcher) Handle(ctx context.Context, update tgbotapi.Update) {
	if update.Message == nil {
		return
	}
	msg := update.Message

	// 鉴权：只处理 allowed_users 中的用户
	if !d.isAllowed(msg.From.ID) {
		slog.Warn("拒绝未授权用户",
			"user_id", msg.From.ID,
			"username", msg.From.UserName,
			"bot", d.botCfg.Name)
		return
	}

	// 处理内置命令
	if msg.IsCommand() {
		d.handleCommand(ctx, msg)
		return
	}

	text := strings.TrimSpace(msg.Text)
	if text == "" {
		return
	}

	d.enqueueWithDebounce(ctx, msg.Chat.ID, incomingMsg{
		text:       text,
		from:       msg.From.UserName,
		chatID:     msg.Chat.ID,
		receivedAt: time.Now(),
	})
}

// handleCommand 处理 /start /help /clear /status 等内置命令。
func (d *Dispatcher) handleCommand(ctx context.Context, msg *tgbotapi.Message) {
	switch msg.Command() {
	case "start", "help":
		d.reply(msg.Chat.ID, "👋 goclaudeclaw 已就绪\n\n"+
			"发送任意消息即可与 Claude 对话。\n"+
			"命令:\n"+
			"  /clear — 清除当前会话\n"+
			"  /status — 查看运行状态\n"+
			"  /bg <任务> — 强制以后台模式运行")
	case "clear":
		// TODO: 调用 session.Manager.Clear()
		d.reply(msg.Chat.ID, "✓ 会话已清除，下次对话将开启新会话。")
	case "status":
		d.reply(msg.Chat.ID, fmt.Sprintf(
			"Bot: %s\nWorkspace: %s\nSecurity: %s",
			d.botCfg.Name, d.workspace, d.cfg.Security.Level,
		))
	case "bg":
		// 强制后台模式
		prompt := msg.CommandArguments()
		if prompt == "" {
			d.reply(msg.Chat.ID, "用法: /bg <任务描述>")
			return
		}
		d.dispatchJob(ctx, msg.Chat.ID, prompt, runner.ModeBackground)
	default:
		d.reply(msg.Chat.ID, "未知命令，发送 /help 查看帮助。")
	}
}

// enqueueWithDebounce 将消息加入防抖窗口。
// 在 debounce_ms 内连续到达的消息会被合并为一条发给 claude。
func (d *Dispatcher) enqueueWithDebounce(ctx context.Context, chatID int64, msg incomingMsg) {
	debounceMs := d.botCfg.DebounceMs
	if debounceMs <= 0 {
		debounceMs = 1500 // 默认 1.5s
	}
	delay := time.Duration(debounceMs) * time.Millisecond

	d.mu.Lock()
	state, ok := d.debounce[chatID]
	if !ok {
		state = &debounceState{}
		d.debounce[chatID] = state
	}
	d.mu.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()

	state.messages = append(state.messages, msg)

	// 重置计时器：新消息到来时重新计时
	if state.timer != nil {
		state.timer.Stop()
	}
	state.timer = time.AfterFunc(delay, func() {
		state.mu.Lock()
		msgs := state.messages
		state.messages = nil
		state.mu.Unlock()

		if len(msgs) == 0 {
			return
		}

		// 合并消息
		combined := combineMessages(msgs)
		slog.Info("防抖窗口触发",
			"chat_id", chatID,
			"message_count", len(msgs),
			"combined_len", len(combined),
			"bot", d.botCfg.Name)

		// 异步分类和分发，不阻塞防抖 goroutine
		go func() {
			mode := d.classifier.Classify(ctx, combined)
			d.dispatchJob(ctx, chatID, combined, mode)
		}()
	})
}

// dispatchJob 将任务提交到 runner，并处理 Telegram 回复。
func (d *Dispatcher) dispatchJob(ctx context.Context, chatID int64, prompt string, mode runner.TaskMode) {
	// 后台任务：立即回复用户，异步执行
	if mode == runner.ModeBackground {
		d.reply(chatID, "⏳ 已在后台处理，完成后通知你。")

		resultCh := make(chan runner.Result, 1)
		d.runnerMgr.Submit(runner.Job{
			Ctx:       ctx,
			Workspace: d.workspace,
			Prompt:    prompt,
			Mode:      mode,
			ResultCh:  resultCh,
		})

		go func() {
			result := <-resultCh
			if result.Err != nil {
				d.reply(chatID, fmt.Sprintf("❌ 后台任务失败: %v", result.Err))
				return
			}
			// 长输出分段发送（Telegram 单条消息限 4096 字符）
			d.sendOutput(chatID, result.Output)
		}()
		return
	}

	// 前台任务：等待结果后回复
	d.reply(chatID, "⏳ 处理中...")

	resultCh := make(chan runner.Result, 1)
	d.runnerMgr.Submit(runner.Job{
		Ctx:       ctx,
		Workspace: d.workspace,
		Prompt:    prompt,
		Mode:      mode,
		ResultCh:  resultCh,
	})

	result := <-resultCh
	if result.Err != nil {
		d.reply(chatID, fmt.Sprintf("❌ 执行失败: %v", result.Err))
		return
	}
	d.sendOutput(chatID, result.Output)
}

// sendOutput 处理超长输出，分段发送（每段最多 4000 字符）。
func (d *Dispatcher) sendOutput(chatID int64, output string) {
	if output == "" {
		d.reply(chatID, "✓ 完成（无输出）")
		return
	}

	const maxLen = 4000
	runes := []rune(output)

	for len(runes) > 0 {
		chunk := runes
		if len(chunk) > maxLen {
			chunk = runes[:maxLen]
			runes = runes[maxLen:]
		} else {
			runes = nil
		}
		d.reply(chatID, string(chunk))
	}
}

// reply 向指定 chat 发送文本消息，错误只记录日志不抛出。
func (d *Dispatcher) reply(chatID int64, text string) {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = tgbotapi.ModeMarkdown
	if _, err := d.botAPI.Send(msg); err != nil {
		slog.Error("发送 Telegram 消息失败",
			"chat_id", chatID,
			"err", err,
			"bot", d.botCfg.Name)
	}
}

// isAllowed 检查用户是否在白名单中。
func (d *Dispatcher) isAllowed(userID int64) bool {
	d.mu.Lock()
	allowed := d.botCfg.AllowedUsers
	d.mu.Unlock()

	for _, id := range allowed {
		if id == userID {
			return true
		}
	}
	return false
}

// combineMessages 将多条消息合并为一条，按时间顺序拼接。
// 多条消息之间用换行分隔，便于 claude 理解上下文。
func combineMessages(msgs []incomingMsg) string {
	if len(msgs) == 1 {
		return msgs[0].text
	}
	var sb strings.Builder
	for i, m := range msgs {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(m.text)
	}
	return sb.String()
}
