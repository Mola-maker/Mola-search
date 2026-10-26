package searchdet

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// hydeDraft asks a language model for a short hypothetical answer, which is
// then searched as a query: the HyDE mode's premise is that an answer-shaped
// passage retrieves better evidence than the question alone. It needs a
// DeepSeek key; without one the caller says so instead of pretending.
func hydeDraft(ctx context.Context, k Keys, query string) (string, error) {
	if strings.TrimSpace(k.DeepSeek) == "" {
		return "", errors.New("没有 DEEPSEEK_API_KEY，HyDE 草稿未生成")
	}
	payload := map[string]any{
		"model": "deepseek-chat",
		"messages": []map[string]string{
			{"role": "system", "content": "你是检索助手。针对用户的问题写一段 80 字以内、像百科条目一样的事实性假设答案。只输出这段文字。"},
			{"role": "user", "content": query},
		},
		"temperature": 0.3,
		"max_tokens":  160,
	}
	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	cctx, cancel := httpDeadline(ctx)
	defer cancel()
	if err := postJSON(cctx, "https://api.deepseek.com/chat/completions", map[string]string{
		"Authorization": "Bearer " + k.DeepSeek,
	}, payload, &out); err != nil {
		return "", fmt.Errorf("HyDE 草稿生成失败: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", errors.New("HyDE 草稿生成失败: 模型没有返回内容")
	}
	draft := strings.TrimSpace(out.Choices[0].Message.Content)
	if draft == "" {
		return "", errors.New("HyDE 草稿生成失败: 返回空文本")
	}
	return draft, nil
}
