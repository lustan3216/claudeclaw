// Package memory 封装 claude-mem / mem0 REST API 调用，
// 为所有 bot 提供统一的共享记忆读写接口。
package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Memory 单条记忆条目。
type Memory struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Score   float64 `json:"score,omitempty"` // 搜索相关度分数
}

// SearchResult 搜索接口返回结果。
type SearchResult struct {
	Memories []Memory `json:"memories"`
}

// Client 与 claude-mem 服务通信的 HTTP 客户端。
type Client struct {
	endpoint   string
	httpClient *http.Client
}

// New 创建一个新的 Client。
// endpoint 示例: "http://localhost:8080"
func New(endpoint string) *Client {
	return &Client{
		endpoint: endpoint,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// Search 根据查询文本搜索相关记忆，返回最多 topK 条结果。
func (c *Client) Search(ctx context.Context, query string, topK int) ([]Memory, error) {
	if topK <= 0 {
		topK = 5
	}
	payload := map[string]any{
		"query": query,
		"top_k": topK,
	}
	var result SearchResult
	if err := c.post(ctx, "/search", payload, &result); err != nil {
		return nil, fmt.Errorf("搜索记忆失败: %w", err)
	}
	slog.Debug("记忆搜索完成", "query", query, "results", len(result.Memories))
	return result.Memories, nil
}

// Add 向 claude-mem 添加一条新记忆。
// content 是自然语言文本，metadata 是可选的附加字段（可传 nil）。
func (c *Client) Add(ctx context.Context, content string, metadata map[string]any) error {
	payload := map[string]any{
		"content":  content,
		"metadata": metadata,
	}
	if err := c.post(ctx, "/add", payload, nil); err != nil {
		return fmt.Errorf("添加记忆失败: %w", err)
	}
	slog.Debug("记忆已添加", "content_len", len(content))
	return nil
}

// Delete 按 ID 删除一条记忆。
func (c *Client) Delete(ctx context.Context, id string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete,
		c.endpoint+"/memories/"+id, nil)
	if err != nil {
		return fmt.Errorf("构建删除请求失败: %w", err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("删除请求失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("删除记忆失败 HTTP %d: %s", resp.StatusCode, string(body))
	}
	return nil
}

// Health 检查 claude-mem 服务是否可用。
func (c *Client) Health(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.endpoint+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("claude-mem 服务不可达: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("claude-mem 健康检查失败 HTTP %d", resp.StatusCode)
	}
	return nil
}

// post 发送 JSON POST 请求，将响应 JSON 解码到 out（out 为 nil 时忽略响应体）。
func (c *Client) post(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("序列化请求体失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		c.endpoint+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("构建请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("HTTP 请求失败: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("读取响应失败: %w", err)
	}

	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if out != nil && len(respBody) > 0 {
		if err := json.Unmarshal(respBody, out); err != nil {
			return fmt.Errorf("解析响应 JSON 失败: %w", err)
		}
	}
	return nil
}
