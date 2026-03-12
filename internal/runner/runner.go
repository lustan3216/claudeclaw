// Package runner 执行 claude CLI 命令，每个 workspace 维护一个串行队列，
// 确保同一目录下的任务按序执行，避免并发写文件冲突。
package runner

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"sync"

	"github.com/lustan3216/goclaudeclaw/internal/config"
	"github.com/lustan3216/goclaudeclaw/internal/session"
)

// Result 包含 claude 执行结果。
type Result struct {
	Output string
	Err    error
}

// Job 是提交给串行队列的任务单元。
type Job struct {
	Ctx       context.Context
	Workspace string
	Prompt    string
	Mode      TaskMode
	ResultCh  chan<- Result // 调用方监听此 channel 获取结果
}

// Manager 管理所有 workspace 的串行执行队列。
// 每个 workspace 对应一个独立的 goroutine + buffered channel。
type Manager struct {
	mu         sync.Mutex
	queues     map[string]chan Job // workspace → job channel
	sessions   *session.Manager
	classifier *Classifier
	cfg        *config.Config
	claudePath string
}

// NewManager 创建 Runner Manager。
func NewManager(cfg *config.Config, sessions *session.Manager, claudePath string) *Manager {
	return &Manager{
		queues:     make(map[string]chan Job),
		sessions:   sessions,
		classifier: NewClassifier(claudePath),
		cfg:        cfg,
		claudePath: claudePath,
	}
}

// UpdateConfig 热重载时更新配置引用（调用方加锁保护）。
func (m *Manager) UpdateConfig(cfg *config.Config) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
}

// Submit 将任务提交到对应 workspace 的串行队列。
// 对于 ModeBackground 任务，resultCh 可传 nil（调用方不等待结果）。
func (m *Manager) Submit(job Job) {
	q := m.getOrCreateQueue(job.Workspace)

	select {
	case q <- job:
		slog.Debug("任务已入队", "workspace", job.Workspace, "mode", job.Mode)
	case <-job.Ctx.Done():
		slog.Warn("任务入队前上下文已取消", "workspace", job.Workspace)
		if job.ResultCh != nil {
			job.ResultCh <- Result{Err: job.Ctx.Err()}
		}
	}
}

// getOrCreateQueue 获取或创建 workspace 对应的串行队列 goroutine。
func (m *Manager) getOrCreateQueue(workspace string) chan Job {
	m.mu.Lock()
	defer m.mu.Unlock()

	if q, ok := m.queues[workspace]; ok {
		return q
	}

	// 缓冲大小 32：允许短时间内积压，防止 Telegram 消息丢失
	q := make(chan Job, 32)
	m.queues[workspace] = q
	go m.runQueue(workspace, q)
	slog.Info("为 workspace 创建串行执行队列", "workspace", workspace)
	return q
}

// runQueue 是每个 workspace 的串行执行 goroutine，
// 按序消费队列中的任务，直到 channel 被关闭。
func (m *Manager) runQueue(workspace string, q <-chan Job) {
	for job := range q {
		result := m.execute(job)
		if job.ResultCh != nil {
			job.ResultCh <- result
		}
	}
}

// execute 实际执行 claude CLI，返回输出结果。
func (m *Manager) execute(job Job) Result {
	sessionID := m.sessions.Get(job.Workspace)

	args := m.buildArgs(job, sessionID)

	slog.Info("执行 claude",
		"workspace", job.Workspace,
		"mode", job.Mode,
		"session_id", sessionID,
		"args_count", len(args))

	cmd := exec.CommandContext(job.Ctx, m.claudePath, args...)
	cmd.Dir = job.Workspace

	// 流式读取输出，实时拼接
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return Result{Err: fmt.Errorf("获取 stdout pipe 失败: %w", err)}
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return Result{Err: fmt.Errorf("获取 stderr pipe 失败: %w", err)}
	}

	if err := cmd.Start(); err != nil {
		return Result{Err: fmt.Errorf("启动 claude 失败: %w", err)}
	}

	// 并发读取 stdout 和 stderr
	var outputBuilder strings.Builder
	var wg sync.WaitGroup

	wg.Add(2)
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stdout)
		for scanner.Scan() {
			line := scanner.Text()
			outputBuilder.WriteString(line)
			outputBuilder.WriteByte('\n')
		}
	}()
	go func() {
		defer wg.Done()
		scanner := bufio.NewScanner(stderr)
		for scanner.Scan() {
			slog.Debug("claude stderr", "line", scanner.Text(), "workspace", job.Workspace)
		}
	}()

	wg.Wait()
	if err := cmd.Wait(); err != nil {
		// 上下文取消是正常情况，不视为错误
		if job.Ctx.Err() != nil {
			return Result{Err: fmt.Errorf("任务被取消: %w", job.Ctx.Err())}
		}
		return Result{
			Output: outputBuilder.String(),
			Err:    fmt.Errorf("claude 退出错误: %w", err),
		}
	}

	output := strings.TrimSpace(outputBuilder.String())

	// 尝试从输出中提取新的 session ID 并持久化
	// claude 在 --resume 模式下会在输出末尾附加 session ID（格式: [session: <id>]）
	if newID := extractSessionID(output); newID != "" && newID != sessionID {
		if err := m.sessions.Set(job.Workspace, newID); err != nil {
			slog.Warn("持久化 session ID 失败", "err", err)
		}
	}

	return Result{Output: output}
}

// buildArgs 根据任务配置组装 claude 命令行参数。
func (m *Manager) buildArgs(job Job, sessionID string) []string {
	m.mu.Lock()
	cfg := m.cfg
	m.mu.Unlock()

	args := []string{}

	// 权限级别映射
	switch cfg.Security.Level {
	case "unrestricted":
		args = append(args, "--dangerously-skip-permissions")
	case "moderate", "strict":
		// moderate/strict 依赖 claude 自身的确认机制，不额外传参
	case "locked":
		// locked 模式：只读，通过 system prompt 约束（TODO: 注入 system prompt）
	}

	// 恢复会话
	if sessionID != "" {
		args = append(args, "--resume", sessionID)
	}

	// 单次 prompt 模式（非交互）
	args = append(args, "-p", job.Prompt)

	// 后台任务：添加输出格式标记，方便日志解析（可根据实际 claude 版本调整）
	if job.Mode == ModeBackground {
		slog.Debug("后台任务，使用静默执行模式")
	}

	return args
}

// extractSessionID 从 claude 输出中提取会话 ID。
// claude 输出格式可能为: [session: abc123] 或 Session ID: abc123
func extractSessionID(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		// 匹配 [session: <id>]
		if strings.HasPrefix(line, "[session:") && strings.HasSuffix(line, "]") {
			id := strings.TrimPrefix(line, "[session:")
			id = strings.TrimSuffix(id, "]")
			return strings.TrimSpace(id)
		}
		// 匹配 Session ID: <id>
		if strings.HasPrefix(line, "Session ID:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "Session ID:"))
		}
	}
	return ""
}
