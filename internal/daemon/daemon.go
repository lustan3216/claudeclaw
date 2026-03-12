// Package daemon 管理进程生命周期：PID 文件、信号处理和优雅关闭。
package daemon

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

// PIDFile 管理 PID 文件的写入和清理。
type PIDFile struct {
	path string
}

// NewPIDFile 创建 PID 文件管理器。
// path 为 PID 文件路径，如 "/var/run/goclaudeclaw.pid"。
func NewPIDFile(path string) *PIDFile {
	return &PIDFile{path: path}
}

// Write 将当前进程 PID 写入文件。
// 如果文件已存在且对应进程仍在运行，返回错误（防止重复启动）。
func (p *PIDFile) Write() error {
	// 检查是否已有实例在运行
	if existing, err := p.readExisting(); err == nil && existing > 0 {
		if isProcessRunning(existing) {
			return fmt.Errorf("已有实例在运行 (PID: %d)，请先停止或删除 %s", existing, p.path)
		}
		// 旧 PID 对应的进程已不存在，安全覆盖
		slog.Info("发现过期 PID 文件，将覆盖", "old_pid", existing)
	}

	// 确保目录存在
	if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err != nil {
		return fmt.Errorf("创建 PID 文件目录失败: %w", err)
	}

	pid := os.Getpid()
	content := strconv.Itoa(pid) + "\n"
	if err := os.WriteFile(p.path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入 PID 文件失败: %w", err)
	}

	slog.Info("PID 文件已创建", "pid", pid, "path", p.path)
	return nil
}

// Remove 删除 PID 文件（程序退出时调用）。
func (p *PIDFile) Remove() {
	if err := os.Remove(p.path); err != nil && !os.IsNotExist(err) {
		slog.Error("删除 PID 文件失败", "path", p.path, "err", err)
		return
	}
	slog.Info("PID 文件已删除", "path", p.path)
}

// readExisting 从已有 PID 文件读取 PID 值。
func (p *PIDFile) readExisting() (int, error) {
	data, err := os.ReadFile(p.path)
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(data)))
	if err != nil {
		return 0, fmt.Errorf("PID 文件格式错误: %w", err)
	}
	return pid, nil
}

// isProcessRunning 检查指定 PID 的进程是否存在（仅 Unix）。
func isProcessRunning(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// 发送信号 0 探测进程是否存在，不实际发送信号
	err = proc.Signal(syscall.Signal(0))
	return err == nil
}

// WaitForShutdown 阻塞直到接收到 SIGINT 或 SIGTERM 信号，
// 然后调用 cancel() 触发优雅关闭流程。
// 返回收到的信号，供调用方记录日志。
func WaitForShutdown(cancel context.CancelFunc) os.Signal {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	sig := <-sigCh
	slog.Info("收到停止信号，开始优雅关闭", "signal", sig)

	// 取消根上下文，触发所有子组件停止
	cancel()

	// 取消 signal 监听，避免第二次信号阻塞
	signal.Stop(sigCh)

	return sig
}

// SetupLogger 初始化结构化日志（slog），根据 debug 标志选择级别。
func SetupLogger(debug bool) {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	handler := slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
		Level:     level,
		AddSource: debug, // debug 模式下附加源码位置
	})
	slog.SetDefault(slog.New(handler))
}
