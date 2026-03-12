// Package session 管理每个 workspace 的 Claude 会话 ID 持久化。
// 会话 ID 存储在 workspace 目录下的 .goclaudeclaw_session 文件中，
// 确保重启后 --resume 标志能恢复上次对话上下文。
package session

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const sessionFileName = ".goclaudeclaw_session"

// Manager 管理多个 workspace 的会话 ID，并发安全。
type Manager struct {
	mu       sync.RWMutex
	sessions map[string]string // workspace → session ID
}

// New 返回一个新的 Manager 实例。
func New() *Manager {
	return &Manager{
		sessions: make(map[string]string),
	}
}

// Get 返回指定 workspace 的会话 ID。
// 优先从内存缓存读取；缓存未命中时从磁盘加载。
// 如果没有已知会话，返回空字符串（调用方应不带 --resume 运行）。
func (m *Manager) Get(workspace string) string {
	m.mu.RLock()
	id, ok := m.sessions[workspace]
	m.mu.RUnlock()
	if ok {
		return id
	}

	// 从磁盘读取，并写入缓存
	id = m.load(workspace)
	if id != "" {
		m.mu.Lock()
		m.sessions[workspace] = id
		m.mu.Unlock()
	}
	return id
}

// Set 更新指定 workspace 的会话 ID，同时持久化到磁盘。
func (m *Manager) Set(workspace, sessionID string) error {
	m.mu.Lock()
	m.sessions[workspace] = sessionID
	m.mu.Unlock()
	return m.save(workspace, sessionID)
}

// Clear 清除 workspace 的会话记录（下次运行将开启新会话）。
func (m *Manager) Clear(workspace string) error {
	m.mu.Lock()
	delete(m.sessions, workspace)
	m.mu.Unlock()

	path := sessionFilePath(workspace)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("清除会话文件失败 %s: %w", path, err)
	}
	slog.Info("会话已清除", "workspace", workspace)
	return nil
}

// load 从磁盘读取会话 ID 文件。
func (m *Manager) load(workspace string) string {
	path := sessionFilePath(workspace)
	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("读取会话文件失败", "path", path, "err", err)
		}
		return ""
	}
	id := strings.TrimSpace(string(data))
	slog.Debug("从磁盘加载会话", "workspace", workspace, "session_id", id)
	return id
}

// save 将会话 ID 写入磁盘（原子写：先写临时文件再重命名）。
func (m *Manager) save(workspace, sessionID string) error {
	path := sessionFilePath(workspace)
	tmpPath := path + ".tmp"

	if err := os.WriteFile(tmpPath, []byte(sessionID+"\n"), 0o600); err != nil {
		return fmt.Errorf("写入临时会话文件失败: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("重命名会话文件失败: %w", err)
	}
	slog.Debug("会话已持久化", "workspace", workspace, "session_id", sessionID)
	return nil
}

// sessionFilePath 返回 workspace 目录下会话文件的完整路径。
func sessionFilePath(workspace string) string {
	return filepath.Join(workspace, sessionFileName)
}
