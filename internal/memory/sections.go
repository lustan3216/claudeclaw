// sections.go 实现 memory.md 的 section 解析与相关性评分。
//
// Section 格式（HTML 注释 header）：
//
//	<!-- section: name tags: tag1,tag2,中文,english -->
//	## Section Title
//	content...
//
// "always" tag 的 section 每次都注入（适合全局偏好等）。
// 其他 section 按 tag 命中数量评分，只注入相关的。
// 若文件没有 section 标记，整体作为 global/always section 降级处理。
package memory

import (
	"regexp"
	"strings"
)

// maxExtraSections 控制 always 之外最多注入几个额外 section。
const maxExtraSections = 3

// maxSectionBytes 单个 section 的字节上限（防止一个超大 section 吃掉所有 token）。
const maxSectionBytes = 1500

var sectionHeaderRe = regexp.MustCompile(
	`<!--\s*section:\s*(\S+)\s+tags:\s*([^-]+?)\s*-->`)

// Section 代表 memory.md 中一个独立记忆片段。
type Section struct {
	Name    string
	Tags    []string // 包含双语同义词，"always" = 每次注入
	Content string
}

// ParseSections 将 memory.md 内容解析为 Section 列表。
// 若未找到 section 标记，返回单个 global/always section（向后兼容）。
func ParseSections(content string) []Section {
	matches := sectionHeaderRe.FindAllStringIndex(content, -1)
	if len(matches) == 0 {
		// 旧格式或手写文件，整体作为 always section
		return []Section{{
			Name:    "global",
			Tags:    []string{"always"},
			Content: strings.TrimSpace(content),
		}}
	}

	sections := make([]Section, 0, len(matches))
	for i, match := range matches {
		sub := sectionHeaderRe.FindStringSubmatch(content[match[0]:match[1]])
		name := sub[1]

		rawTags := strings.Split(sub[2], ",")
		tags := make([]string, 0, len(rawTags))
		for _, t := range rawTags {
			t = strings.ToLower(strings.TrimSpace(t))
			if t != "" {
				tags = append(tags, t)
			}
		}

		// section 内容：从 header 结束到下一个 header 开始
		start := match[1]
		end := len(content)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		body := strings.TrimSpace(content[start:end])
		if len(body) > maxSectionBytes {
			body = body[:maxSectionBytes] + "\n...(truncated)"
		}

		sections = append(sections, Section{Name: name, Tags: tags, Content: body})
	}
	return sections
}

// SelectRelevant 根据 prompt 关键词选出相关 sections。
//
//   - "always" tag 的 section 始终包含
//   - 其他 section 按 tag 命中数量排序，取 top maxExtraSections 个
func SelectRelevant(sections []Section, prompt string) []Section {
	promptLower := strings.ToLower(prompt)

	type candidate struct {
		sec   Section
		score int
	}

	var always []Section
	var others []candidate

	for _, sec := range sections {
		isAlways := false
		for _, t := range sec.Tags {
			if t == "always" {
				isAlways = true
				break
			}
		}
		if isAlways {
			always = append(always, sec)
			continue
		}

		// 计算 tag 命中分数（每个 tag 出现在 prompt 中算 1 分）
		score := 0
		for _, t := range sec.Tags {
			if t != "" && strings.Contains(promptLower, t) {
				score++
			}
		}
		if score > 0 {
			others = append(others, candidate{sec, score})
		}
	}

	// 按分数降序排序（sections 数量通常很少，简单插入排序即可）
	for i := 1; i < len(others); i++ {
		for j := i; j > 0 && others[j].score > others[j-1].score; j-- {
			others[j], others[j-1] = others[j-1], others[j]
		}
	}

	result := make([]Section, 0, len(always)+maxExtraSections)
	result = append(result, always...)
	for i, c := range others {
		if i >= maxExtraSections {
			break
		}
		result = append(result, c.sec)
	}
	return result
}

// BuildInjection 将选中的 sections 内容拼接为注入字符串。
func BuildInjection(sections []Section) string {
	if len(sections) == 0 {
		return ""
	}
	parts := make([]string, 0, len(sections))
	for _, s := range sections {
		if s.Content != "" {
			parts = append(parts, s.Content)
		}
	}
	return strings.Join(parts, "\n\n")
}
