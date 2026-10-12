package bytedp_memory

import (
	"fmt"
	"strings"
	"time"
)

// Helper functions for SQL placeholders
func sourcePlaceholders(sources []MemorySource) string {
	if len(sources) == 0 {
		return "'longterm', 'session', 'daily'"
	}
	placeholders := make([]string, len(sources))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return "(" + joinString(placeholders, ",") + ")"
}

func typePlaceholders(types []MemoryType) string {
	if len(types) == 0 {
		// 默认覆盖全部类型：旧四类型（兼容存量数据）+ 新三类型（memdir 长期记忆）
		return "'fact', 'preference', 'context', 'conversation', 'user', 'feedback', 'reference'"
	}
	placeholders := make([]string, len(types))
	for i := range placeholders {
		placeholders[i] = "?"
	}
	return "(" + joinString(placeholders, ",") + ")"
}

func appendSources(args []interface{}, sources []MemorySource) []interface{} {
	if len(sources) == 0 {
		return args
	}
	for _, s := range sources {
		args = append(args, string(s))
	}
	return args
}

func appendTypes(args []interface{}, types []MemoryType) []interface{} {
	if len(types) == 0 {
		return args
	}
	for _, t := range types {
		args = append(args, string(t))
	}
	return args
}

// sessionKeyFilter 构建会话隔离过滤片段与参数：
// 全局记忆（session_key 为 NULL）始终放行，会话记忆必须属于给定会话。
// keys 为空时不产生任何过滤（跨会话全局召回）。
func sessionKeyFilter(keys []string) (string, []interface{}) {
	if len(keys) == 0 {
		return "", nil
	}
	placeholders := make([]string, len(keys))
	args := make([]interface{}, len(keys))
	for i, k := range keys {
		placeholders[i] = "?"
		args[i] = k
	}
	return "AND (m.session_key IS NULL OR m.session_key IN (" + joinString(placeholders, ",") + "))\n", args
}

// afterDateFilter returns the SQL fragment and args for AfterDate filtering
// (memories updated at or after the given time).
func afterDateFilter(after *time.Time) (string, []interface{}) {
	if after == nil {
		return "", nil
	}
	return "AND m.updated_at >= ?\n", []interface{}{after.Unix()}
}

// relationFilter builds the SQL fragment and args filtering memories to
// those declaring an outgoing relation. targetCandidates are the resolved
// target texts (raw wiki-link target plus permalink/name aliases); the
// fragment matches when the relation's target is any of them, optionally
// restricted to one relation type.
func relationFilter(relationType string, targetCandidates []string) (string, []interface{}) {
	if relationType == "" && len(targetCandidates) == 0 {
		return "", nil
	}
	var b strings.Builder
	b.WriteString("AND m.file_path IN (\n\tSELECT DISTINCT file_path FROM relations\n\tWHERE 1=1")
	args := []interface{}{}
	if relationType != "" {
		b.WriteString("\n\tAND relation_type = ?")
		args = append(args, relationType)
	}
	if len(targetCandidates) > 0 {
		placeholders := make([]string, len(targetCandidates))
		for i := range targetCandidates {
			placeholders[i] = "?"
		}
		b.WriteString("\n\tAND target IN (" + joinString(placeholders, ",") + ")")
		for _, c := range targetCandidates {
			args = append(args, c)
		}
	}
	b.WriteString("\n)\n")
	return b.String(), args
}

func float32SliceToString(vec []float32) string {
	str := make([]byte, 0, len(vec)*8+2)
	str = append(str, '[')
	for i, v := range vec {
		if i > 0 {
			str = append(str, ',')
		}
		str = append(str, fmt.Sprintf("%f", v)...)
	}
	str = append(str, ']')
	return string(str)
}

func joinString(strs []string, sep string) string {
	if len(strs) == 0 {
		return ""
	}
	if len(strs) == 1 {
		return strs[0]
	}
	result := strs[0]
	for i := 1; i < len(strs); i++ {
		result += sep + strs[i]
	}
	return result
}
