// Package memory 包含 CLAUDE.md 管理功能。
// 维护 workspace 目录下 CLAUDE.md 文件中的受管理块（managed block），
// 用于向 Claude 注入 bot 相关的系统级上下文，而不覆盖用户自定义内容。
package memory

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const (
	// ManagedBlockStart 受管理块的起始标记。
	ManagedBlockStart = "<!-- goclaudeclaw:managed:start -->"
	// ManagedBlockEnd 受管理块的结束标记。
	ManagedBlockEnd = "<!-- goclaudeclaw:managed:end -->"

	claudeMDFilename = "CLAUDE.md"
)

// EnsureClaudeMD 确保 workspace 下存在 CLAUDE.md，并包含受管理块。
// - 若文件不存在：创建并写入受管理块。
// - 若文件已存在但没有受管理块：在末尾追加受管理块。
// - 若文件已存在且有受管理块：替换受管理块内容。
// 文件中受管理块外的内容始终保留，不会被覆盖。
func EnsureClaudeMD(workspace string, content string) error {
	path := filepath.Join(workspace, claudeMDFilename)

	existing, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("读取 CLAUDE.md 失败: %w", err)
		}
		// 文件不存在，直接创建
		return writeClaudeMD(path, buildManagedBlock(content))
	}

	// 文件已存在，替换或追加受管理块
	merged := mergeManagedBlock(string(existing), content)
	return writeClaudeMD(path, merged)
}

// UpdateManagedBlock 仅更新 CLAUDE.md 中受管理块的内容。
// 如果文件或受管理块不存在，等同于 EnsureClaudeMD。
func UpdateManagedBlock(workspace string, content string) error {
	return EnsureClaudeMD(workspace, content)
}

// ReadManagedBlock 读取 CLAUDE.md 中受管理块的内容。
// 若文件不存在或没有受管理块，返回空字符串。
func ReadManagedBlock(workspace string) string {
	path := filepath.Join(workspace, claudeMDFilename)
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	_, inner, ok := extractManagedBlock(string(raw))
	if !ok {
		return ""
	}
	return strings.TrimSpace(inner)
}

// mergeManagedBlock 在现有文件内容中替换受管理块；如不存在则追加。
func mergeManagedBlock(existing string, newContent string) string {
	before, _, ok := extractManagedBlock(existing)
	if !ok {
		// 没有受管理块，追加到末尾
		trimmed := strings.TrimRight(existing, "\n")
		return trimmed + "\n\n" + buildManagedBlock(newContent) + "\n"
	}

	// 找到结束标记之后的内容
	endIdx := strings.Index(existing, ManagedBlockEnd)
	after := ""
	if endIdx >= 0 {
		after = existing[endIdx+len(ManagedBlockEnd):]
	}

	// 重新组合：before + 新的受管理块 + after
	result := strings.TrimRight(before, "\n")
	if result != "" {
		result += "\n\n"
	}
	result += buildManagedBlock(newContent)
	afterTrimmed := strings.TrimLeft(after, "\n")
	if afterTrimmed != "" {
		result += "\n\n" + afterTrimmed
	} else {
		result += "\n"
	}
	return result
}

// extractManagedBlock 从文件内容中提取受管理块。
// 返回 before（块前内容）、inner（块内内容）、ok（是否找到块）。
func extractManagedBlock(content string) (before, inner string, ok bool) {
	startIdx := strings.Index(content, ManagedBlockStart)
	if startIdx < 0 {
		return content, "", false
	}
	endIdx := strings.Index(content, ManagedBlockEnd)
	if endIdx < 0 || endIdx < startIdx {
		return content, "", false
	}
	before = content[:startIdx]
	inner = content[startIdx+len(ManagedBlockStart) : endIdx]
	return before, inner, true
}

// buildManagedBlock 构建完整的受管理块字符串。
func buildManagedBlock(content string) string {
	return ManagedBlockStart + "\n" + strings.TrimSpace(content) + "\n" + ManagedBlockEnd
}

// writeClaudeMD 原子写入 CLAUDE.md（先写临时文件再重命名）。
func writeClaudeMD(path string, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建目录失败: %w", err)
	}
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0o644); err != nil {
		return fmt.Errorf("写入临时 CLAUDE.md 失败: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("重命名 CLAUDE.md 失败: %w", err)
	}
	return nil
}
