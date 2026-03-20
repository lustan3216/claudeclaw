// Package runner — subagent output file tailing.
// 监视 Claude Code subagent 的 output file，解析 NDJSON 提取 text 内容，
// 跳过 thinking 和 tool_use 块，只向 dispatcher 发送实际输出文本。
package runner

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SubagentTailer watches a Claude Code tasks directory for new .output files,
// tails the latest one, and sends extracted text content through TextCh.
type SubagentTailer struct {
	tasksDir string
	ctx      context.Context
	cancel   context.CancelFunc
	textCh   chan string
}

// NewSubagentTailer creates a tailer for the given session's tasks directory.
// Call Start() to begin watching/tailing in a goroutine.
func NewSubagentTailer(parentCtx context.Context, workspace, sessionID string) *SubagentTailer {
	ctx, cancel := context.WithCancel(parentCtx)
	// 将 workspace 路径转换为 temp 目录格式: /data/workspace-4 → -data-workspace-4
	dashed := strings.ReplaceAll(workspace, "/", "-")
	tasksDir := fmt.Sprintf("/tmp/claude-%d/%s/%s/tasks/", os.Getuid(), dashed, sessionID)

	return &SubagentTailer{
		tasksDir: tasksDir,
		ctx:      ctx,
		cancel:   cancel,
		textCh:   make(chan string, 8),
	}
}

// TextCh returns the channel that receives extracted text content from the subagent.
func (t *SubagentTailer) TextCh() <-chan string {
	return t.textCh
}

// Stop cancels tailing and closes the text channel.
func (t *SubagentTailer) Stop() {
	t.cancel()
}

// Start begins watching for new output files and tailing them.
// Blocks until cancelled or the context is done. Run in a goroutine.
func (t *SubagentTailer) Start() {
	defer close(t.textCh)

	slog.Debug("subagent tailer starting", "tasks_dir", t.tasksDir)

	// 等待 tasks 目录出现 (最多 15 秒)
	if !t.waitForDir(15 * time.Second) {
		slog.Debug("subagent tailer: tasks dir never appeared", "dir", t.tasksDir)
		return
	}

	// 快照现有文件，只关注之后新增的
	existing := t.snapshotFiles()

	// 等待新文件出现 (最多 60 秒)
	outputFile := t.waitForNewFile(existing, 60*time.Second)
	if outputFile == "" {
		slog.Debug("subagent tailer: no new output file appeared")
		return
	}

	slog.Debug("subagent tailer: tailing output file", "file", outputFile)
	t.tailFile(outputFile)
}

// waitForDir polls until the tasks directory exists or timeout.
func (t *SubagentTailer) waitForDir(timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if t.ctx.Err() != nil {
			return false
		}
		if info, err := os.Stat(t.tasksDir); err == nil && info.IsDir() {
			return true
		}
		time.Sleep(500 * time.Millisecond)
	}
	return false
}

// snapshotFiles returns the set of filenames currently in the tasks directory.
func (t *SubagentTailer) snapshotFiles() map[string]bool {
	result := make(map[string]bool)
	entries, err := os.ReadDir(t.tasksDir)
	if err != nil {
		return result
	}
	for _, e := range entries {
		result[e.Name()] = true
	}
	return result
}

// waitForNewFile polls the tasks directory for a new .output file not in the existing set.
func (t *SubagentTailer) waitForNewFile(existing map[string]bool, timeout time.Duration) string {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if t.ctx.Err() != nil {
			return ""
		}
		entries, err := os.ReadDir(t.tasksDir)
		if err == nil {
			for _, e := range entries {
				name := e.Name()
				if !existing[name] && strings.HasSuffix(name, ".output") {
					return filepath.Join(t.tasksDir, name)
				}
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	return ""
}

// tailFile reads the output file in a poll loop, extracting text content from assistant messages.
func (t *SubagentTailer) tailFile(path string) {
	var offset int64

	for {
		if t.ctx.Err() != nil {
			return
		}

		f, err := os.Open(path)
		if err != nil {
			time.Sleep(500 * time.Millisecond)
			continue
		}

		if offset > 0 {
			if _, err := f.Seek(offset, 0); err != nil {
				f.Close()
				time.Sleep(500 * time.Millisecond)
				continue
			}
		}

		scanner := bufio.NewScanner(f)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Bytes()
			// 手动追踪 offset: 每行长度 + 换行符
			offset += int64(len(line)) + 1
			if text := extractSubagentText(line); text != "" {
				// 非阻塞发送，丢弃旧的如果 channel 满了
				select {
				case t.textCh <- text:
				default:
					// drain and push new
					select {
					case <-t.textCh:
					default:
					}
					select {
					case t.textCh <- text:
					default:
					}
				}
			}
		}

		f.Close()
		time.Sleep(500 * time.Millisecond)
	}
}

// subagentLine is the minimal structure for parsing an output file NDJSON line.
type subagentLine struct {
	Type    string          `json:"type"`
	Message json.RawMessage `json:"message"`
}

// subagentMessage represents the message envelope in an output file line.
type subagentMessage struct {
	Content []subagentContentBlock `json:"content"`
}

// subagentContentBlock represents a single content block in the message.
type subagentContentBlock struct {
	Type string `json:"type"` // "text", "thinking", "tool_use", etc.
	Text string `json:"text,omitempty"`
}

// extractSubagentText parses an NDJSON line from a subagent output file
// and returns the last text block content from assistant messages.
// Returns empty string for non-assistant messages, thinking blocks, and tool_use blocks.
func extractSubagentText(line []byte) string {
	if len(line) == 0 || line[0] != '{' {
		return ""
	}

	var sl subagentLine
	if err := json.Unmarshal(line, &sl); err != nil {
		return ""
	}

	// 只处理 assistant 消息
	if sl.Type != "assistant" {
		return ""
	}

	var msg subagentMessage
	if err := json.Unmarshal(sl.Message, &msg); err != nil {
		return ""
	}

	// 取最后一个 text block (最新的输出)
	var lastText string
	for _, block := range msg.Content {
		if block.Type == "text" && block.Text != "" {
			lastText = block.Text
		}
	}

	return lastText
}
