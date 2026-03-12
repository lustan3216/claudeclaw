// Package bot 实现每个 Telegram Bot 的长轮询 goroutine。
// 每个 bot 独立运行，共享 runner.Manager 和 session.Manager。
package bot

import (
	"context"
	"log/slog"
	"sync"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/lustan3216/goclaudeclaw/internal/config"
	"github.com/lustan3216/goclaudeclaw/internal/runner"
	"github.com/lustan3216/goclaudeclaw/internal/session"
)

// Bot 封装单个 Telegram bot 的生命周期。
type Bot struct {
	api        *tgbotapi.BotAPI
	cfg        config.BotConfig
	dispatcher *Dispatcher
}

// NewBot 初始化 Bot，建立 Telegram API 连接。
func NewBot(
	botCfg config.BotConfig,
	globalCfg *config.Config,
	runnerMgr *runner.Manager,
	sessionMgr *session.Manager,
	workspace string,
) (*Bot, error) {
	api, err := tgbotapi.NewBotAPI(botCfg.Token)
	if err != nil {
		return nil, err
	}

	// 若 bot 配置未设置 name，使用 Telegram 返回的 username 作为默认值
	if botCfg.Name == "" {
		botCfg.Name = api.Self.UserName
	}

	// 生产环境关闭调试日志，避免 token 泄漏
	api.Debug = false

	slog.Info("Telegram bot 已连接",
		"bot_name", botCfg.Name,
		"username", api.Self.UserName)

	dispatcher := NewDispatcher(api, botCfg, globalCfg, runnerMgr, sessionMgr, workspace)

	return &Bot{
		api:        api,
		cfg:        botCfg,
		dispatcher: dispatcher,
	}, nil
}

// UpdateConfig 热重载：更新 bot 配置（不重建连接）。
func (b *Bot) UpdateConfig(cfg *config.Config) {
	// 找到对应名称的 bot 配置
	for _, bc := range cfg.Bots {
		if bc.Name == b.cfg.Name {
			b.cfg = bc
			b.dispatcher.UpdateConfig(cfg, bc)
			return
		}
	}
}

// Run 启动长轮询循环，阻塞直到 ctx 取消。
// 应在独立 goroutine 中调用。
func (b *Bot) Run(ctx context.Context) {
	slog.Info("启动 bot 长轮询", "bot", b.cfg.Name)

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60 // Telegram 长轮询超时（秒），网络空闲时保持连接

	updates := b.api.GetUpdatesChan(u)

	for {
		select {
		case <-ctx.Done():
			slog.Info("bot 收到停止信号，退出长轮询", "bot", b.cfg.Name)
			b.api.StopReceivingUpdates()
			return

		case update, ok := <-updates:
			if !ok {
				// channel 被关闭，尝试重连
				slog.Warn("更新 channel 已关闭，3 秒后重连", "bot", b.cfg.Name)
				select {
				case <-ctx.Done():
					return
				case <-time.After(3 * time.Second):
					updates = b.reconnect(ctx)
					if updates == nil {
						return // ctx 已取消
					}
				}
				continue
			}

			// 在独立 goroutine 处理，防止单条消息处理慢影响轮询
			// 注意：dispatcher 内部有防抖和串行队列，不会产生并发冲突
			go b.dispatcher.Handle(ctx, update)
		}
	}
}

// reconnect 重新建立 Telegram 更新 channel，带指数退避重试。
// 返回新 channel，如果 ctx 被取消则返回 nil。
func (b *Bot) reconnect(ctx context.Context) tgbotapi.UpdatesChannel {
	select {
	case <-ctx.Done():
		return nil
	default:
	}

	u := tgbotapi.NewUpdate(0)
	u.Timeout = 60
	updates := b.api.GetUpdatesChan(u)
	slog.Info("bot 重连成功", "bot", b.cfg.Name)
	return updates
}

// Manager 管理所有 bot 实例的生命周期。
type Manager struct {
	bots []*Bot
}

// NewManager 根据配置初始化所有 bot 实例。
func NewManager(
	cfg *config.Config,
	runnerMgr *runner.Manager,
	sessionMgr *session.Manager,
) (*Manager, error) {
	var bots []*Bot
	for _, botCfg := range cfg.Bots {
		b, err := NewBot(botCfg, cfg, runnerMgr, sessionMgr, cfg.Workspace)
		if err != nil {
			return nil, err
		}
		bots = append(bots, b)
	}
	return &Manager{bots: bots}, nil
}

// Run 并发启动所有 bot，阻塞直到所有 bot goroutine 退出。
// 通常由 ctx 取消触发退出。
func (m *Manager) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for _, b := range m.bots {
		wg.Add(1)
		bot := b
		go func() {
			defer wg.Done()
			bot.Run(ctx)
		}()
	}
	wg.Wait()
	slog.Info("所有 bot 已退出")
}

// UpdateConfig 向所有 bot 广播配置更新。
func (m *Manager) UpdateConfig(cfg *config.Config) {
	for _, b := range m.bots {
		b.UpdateConfig(cfg)
	}
}
