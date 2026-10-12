# bytedp_memory




Agent 记忆检索系统：SQLite + FTS5 trigram 全文检索 + sqlite-vec 向量语义搜索（CGo-free）+ 混合检索 + MMR 重排 + 时间衰减 + TTL 治理 + 结构化关系图谱 + 实体图谱图遍历。

模块路径：`github.com/iqyeah/bytedp_memory`。

## 能力

- **写入**：`AddMemory` / `AddMemoryBatch`（自动生成 embedding）、观察行幂等 upsert、memdir 文件指纹增量同步（`Indexer`）
- **语义搜索**：向量 embedding 相似度检索（sqlite-vec 内置打包，零 CGO 依赖，交叉编译友好）
- **关键词检索**：FTS5 trigram 分词，CJK 感知（strict → relaxed → LIKE 三级回退）
- **混合检索**：文本 + 向量双通道融合（默认权重 0.7 / 0.3）
- **MMR 重排**：最大边际相关性去重
- **时间衰减**：记忆新鲜度加权（半衰期可配）
- **结构化关系**：wiki-link 关系（`- type [[Target]]`）入库，支持按类型/目标过滤与双向查询
- **实体图谱**：entities 实体派生层（link/llm 双通道 upsert）+ BFS N 跳图遍历（`TraverseRelations`）+ 模糊消歧反查
- **过滤**：来源 / 类型 / 会话隔离 / 时间窗（`AfterDate`）/ 元数据精确匹配（`MetadataFilters`）
- **检索追踪**：`SearchTrace` 输出模式、回退触发与命中诊断
- **TTL 清理**：过期记忆自动删除（`Importance >= 0.8` 可豁免）
- **LRU 缓存**：热查询缓存
- **超时锁**：带超时的读写锁（防死锁）

## 安装

```bash
go get github.com/iqyeah/bytedp_memory
```

## 快速开始

```go
package main

import (
	"context"
	"fmt"

	memory "github.com/iqyeah/bytedp_memory"
)

func main() {
	ctx := context.Background()

	// 1. 创建 embedding provider（OpenAI 兼容接口，示例用 DashScope）
	cfg := memory.DefaultDashScopeConfig("sk-xxx", "https://dashscope.aliyuncs.com/compatible-mode/v1", "text-embedding-v3")
	provider, err := memory.NewOpenAIProvider(cfg)
	if err != nil {
		panic(err)
	}

	// 2. 创建 SQLite 存储
	store, err := memory.NewSQLiteStore(memory.DefaultStoreConfig("./memory.db", provider))
	if err != nil {
		panic(err)
	}
	defer store.Close()

	// 3. 创建检索管理器
	mgr, err := memory.NewMemoryManager(memory.DefaultManagerConfig(store, provider))
	if err != nil {
		panic(err)
	}

	// 4. 写入记忆
	_, err = mgr.AddMemory(ctx, "用户关注联想商城的比价省钱", memory.MemorySourceLongTerm, memory.MemoryTypeUser, memory.MemoryMetadata{
		Description: "用户目标：比价省钱",
	})
	if err != nil {
		panic(err)
	}

	// 5. 关键词检索（FTS5 trigram，CJK 感知，不依赖向量）
	results, err := mgr.Search(ctx, "", memory.SearchOptions{TextQuery: "比价", Limit: 5})
	if err != nil {
		panic(err)
	}
	for _, r := range results {
		fmt.Printf("%.3f %s\n", r.Score, r.Text)
	}
}
```

## 架构分层

| 层 | 类型 | 职责 |
|---|---|---|
| 文件层 | `memdir` 子包 | 三类型长期记忆（user/feedback/reference）Markdown 文件 + frontmatter + MEMORY.md 索引，**文件为真源** |
| 存储层 | `SQLiteStore` | SQLite 落库：memories 表 + vec0 向量表 + FTS5 trigram 全文表 + relations 关系表 + entities 实体表（schema_version 水位版本化迁移） |
| 检索层 | `MemoryManager` | embedding 生成、写入、检索编排、后处理（metadata 过滤/MMR/衰减/MinScore/Limit）、LRU 缓存 |
| 同步层 | `Indexer` | memdir 文件 → SQLite 的增量同步（内容指纹 diff，未变化部分不重新 embedding） |

## 核心概念

### 记忆条目 `VectorEmbedding`

每条记忆 = 文本 + 向量 + 来源 + 类型 + 元数据：

```go
type VectorEmbedding struct {
	ID        string         // 自动生成
	Vector    []float32      // embedding 向量
	Text      string         // 记忆内容（FTS 索引 + 检索返回）
	Source    MemorySource   // 来源
	Type      MemoryType     // 类型
	CreatedAt time.Time
	UpdatedAt time.Time
	Metadata  MemoryMetadata
}
```

### 来源 `MemorySource`

| 值 | 含义 |
|---|---|
| `MemorySourceLongTerm` (`"longterm"`) | 长期记忆（memdir 文件同步） |
| `MemorySourceSession` (`"session"`) | 会话记忆 |
| `MemorySourceDaily` (`"daily"`) | 每日笔记 |

### 类型 `MemoryType`

新三类型（memdir 长期记忆体系）：

| 值 | 含义 |
|---|---|
| `MemoryTypeUser` (`"user"`) | 用户角色/偏好/目标/关注领域 |
| `MemoryTypeFeedback` (`"feedback"`) | 纠正与确认（怎么做事的反馈） |
| `MemoryTypeReference` (`"reference"`) | 外部系统指针（登录入口/面板/数据源） |

旧四类型（保留兼容存量数据，不再写入）：`MemoryTypeFact` / `MemoryTypePreference` / `MemoryTypeContext` / `MemoryTypeConversation`。

### 元数据 `MemoryMetadata`

| 字段 | 用途 |
|---|---|
| `FilePath` | 源文件路径（memdir 记忆指向 `memory/memdir/xxx.md`） |
| `LineNumber` / `EndLineNumber` | 源文件行号范围（citation 用） |
| `SessionKey` | 会话标识（会话隔离过滤用） |
| `Tags` | 标签 |
| `Importance` | 重要性 0-1（TTL 保留判断用，≥0.8 可豁免清理） |
| `Description` | 单行相关性描述（LLM 精选筛选用，对应 memdir frontmatter） |
| `MtimeMs` | 源文件修改时间毫秒（新鲜度标注用） |
| `Timestamp` | 原始时间戳（会话/日记记忆） |
| `Permalink` | 记忆文件的稳定标识（frontmatter permalink，缺省回退文件名） |
| `ObservationKey` | 观察行稳定哈希（category+content），唯一索引幂等 upsert 键 |
| `EntityMetadata` | 自定义 frontmatter 字段（key→value），metadata 过滤用 |

### 检索结果 `SearchResult`

`VectorEmbedding` 内嵌 + `Score`（0-1 相似度）+ `MatchedText` + `Highlight` + `VectorScore`/`TextScore`（混合检索两通道分）+ `Citation` + `AgeInDays`。

### 关系 `RelationRecord`

一条 `- type [[Target]]` 关系行的结构化投影：

```go
type RelationRecord struct {
	FilePath       string // 声明关系的源文件（出边）
	SourcePermalink string // 源文件稳定标识
	Type           string // 关系标签："relates_to"、"implements"、"links_to"…
	Target         string // wiki-link 目标文本（目标记忆的标题或 permalink）
	Context        string // 行尾 "(context)" 补充说明
	LineNumber     int    // 源文件行号（1-based）
	MtimeMs        int64  // 源文件修改时间毫秒
}
```

## 写入记忆

### 通过 MemoryManager（推荐）

```go
// 单条写入：自动生成 embedding
ve, err := mgr.AddMemory(ctx, text, source, memType, metadata)

// 批量写入：一次 embedding 调用
items := []memory.MemoryItem{
	{Text: "记忆A", Source: memory.MemorySourceLongTerm, Type: memory.MemoryTypeUser, Metadata: metadataA},
	{Text: "记忆B", Source: memory.MemorySourceLongTerm, Type: memory.MemoryTypeReference, Metadata: metadataB},
}
err := mgr.AddMemoryBatch(ctx, items)
```

### 分块写入

长文本请先分块再逐块写入（`AddMemory` 不做自动分块）：

```go
// token 估算分块（兼容保留）
for i, chunk := range memory.ChunkText(longText, 500) {
	mgr.AddMemory(ctx, chunk, source, memType, memory.MemoryMetadata{LineNumber: i + 1})
}

// Markdown 感知分块（推荐）：标题为唯一结构边界 → 预算内打包 → 行边界切分 →
// 超长单行字符窗口带 overlap；maxChars<=0 默认 2000，overlap 越界自动归零
for _, chunk := range memory.ChunkMarkdown(longMarkdown, 2000, 200) {
	mgr.AddMemory(ctx, chunk, source, memType, memory.MemoryMetadata{})
}
```

### 观察行幂等写入

观察行（`- [category] content`）建议带 `ObservationKey` 写入，靠唯一索引幂等 upsert：

```go
// 观察键 = category+content 哈希（可自行生成，如 sha256(category+"\x00"+content)）
meta := memory.MemoryMetadata{
	ObservationKey: obsKey,
	LineNumber:     lineNo,
	Permalink:      "memdir/user-goal.md",
}
mgr.AddMemory(ctx, "[preference] prefers dark mode", memory.MemorySourceLongTerm, memory.MemoryTypeUser, meta)
// 同 key 重复写入：ON CONFLICT(observation_key) DO NOTHING，静默跳过，不产生重复行
```

### memdir 文件同步（`Indexer`，文件为真源）

长期记忆推荐落在 `memdir` 文件中，用 `Indexer` 增量同步到索引（内容指纹 diff：未变化的 chunk/观察行/关系行保留原键、**不重新 embedding**，消失的行自动清理）：

```go
// memoryDir 是包含 "memdir" 子目录的目录
ix := memory.NewIndexer(mgr, "/workspace/memory",
	memory.WithChunkSize(2000, 200),          // 分块预算与 overlap
	memory.WithLogf(log.Printf),               // 同步告警日志
)

// 单文件同步：relPath 相对 memoryDir（如 "memdir/user-foo.md"）
if err := ix.SyncFile(ctx, "memdir/user-foo.md"); err != nil { /* ... */ }

// 全量同步：扫描全部文件 + 清理已消失文件的行（meta 表 watermark 记录上次同步时间）
if err := ix.SyncAll(ctx); err != nil { /* ... */ }
```

同步时自动完成：frontmatter（kind/description/permalink/自定义字段）→ metadata；正文 `ChunkMarkdown` 分块索引；`- [category] content` 观察行带 `ObservationKey` 逐行索引；`- type [[Target]]` 关系行双写（FTS 文本行 + 结构化 relations 表）。

### 通过底层 Store

```go
store.Add(ve)             // 插入
store.Update(ve)          // 按 ID 更新（含向量重写）
store.Delete(id)          // 按 ID 删除（同步清理向量表与 FTS 表）
store.List(filter)        // 全量列出，filter 返回 true 保留
store.Get(id)             // 按 ID 读取
```

### 更新与删除（MemoryManager）

```go
mgr.Get(ctx, id)
mgr.Update(ctx, ve)
mgr.Delete(ctx, id)
```

## 检索记忆

### `SearchOptions` 全字段

| 字段 | 说明 |
|---|---|
| `Limit` | 返回条数上限 |
| `MinScore` | 最低相似度（0-1），低于者过滤 |
| `Sources` | 来源过滤；空 = 全部（longterm/session/daily） |
| `Types` | 类型过滤；空 = 全部（含新三类型与旧四类型） |
| `TextQuery` | 非空时走纯 FTS5 trigram 关键词路径，绕过向量 |
| `Hybrid` | 混合检索开关（向量 + FTS 融合） |
| `VectorWeight` / `TextWeight` | 混合权重（默认 0.7 / 0.3） |
| `MMR` | MMR 重排配置（见下） |
| `TemporalDecay` | 时间衰减配置（见下） |
| `IncludeCitations` | 结果附 `Citation` 引用串 |
| `SessionKeys` | 会话隔离过滤（见下） |
| `AfterDate` | 只返回 `updated_at >= AfterDate` 的记忆（配合 `ParseTimeframe`） |
| `MetadataFilters` | 字段→值精确匹配（Go 后过滤，见下） |
| `RelationType` | 只返回声明了指定类型出边关系的记忆（如 `"implements"`） |
| `RelationTarget` | 只返回出边关系指向指定目标的记忆（支持 wiki-link 原文 / permalink / name 消歧匹配） |
| `Trace` | 检索追踪诊断（见下） |

### 三种检索路径

```go
// 1) 纯关键词（FTS5 trigram，CJK 感知；不依赖 embedding）
results, _ := mgr.Search(ctx, "", memory.SearchOptions{TextQuery: "浏览器 自动化", Limit: 10})

// 2) 纯向量（语义，sqlite-vec KNN）
results, _ = mgr.Search(ctx, "如何登录商城", memory.SearchOptions{Limit: 10, MinScore: 0.5})

// 3) 混合（推荐：召回更全）
results, _ = mgr.Search(ctx, "比价", memory.SearchOptions{
	Limit:        20,
	Hybrid:       true,
	VectorWeight: 0.7,
	TextWeight:   0.3,
})
```

### 后处理管线

固定顺序：**metadata 过滤 → 时间衰减 → MMR 重排 → MinScore 过滤 → Limit 截断**。

### MMR 重排（去重多样性）

```go
mmr := memory.DefaultMMRConfig()
mmr.Enabled = true
mmr.Lambda = 0.7 // 0=最大多样性，1=最大相关性
opts.MMR = &mmr
```

### 时间衰减（新鲜度加权）

```go
decay := memory.DefaultTemporalDecayConfig()
decay.Enabled = true
decay.HalfLifeDays = 30 // 半衰期：30 天前的记忆得分折半
opts.TemporalDecay = &decay
```

豁免规则：`memory/MEMORY.md`、`USER.md`、`SOUL.md`、`AGENTS.md`、`IDENTITY.md` 等常青文件路径不受衰减。

### 会话隔离（`SessionKeys`）

设置后，SQL 过滤语义为：**全局记忆（session_key 为 NULL）始终放行 + 仅放行指定会话的记忆**，其他会话记忆被排除，防止跨会话污染：

```go
opts.SessionKeys = []string{currentSessionID}
```

留空则不做任何会话过滤（跨会话全局召回）。

### 时间窗过滤（`AfterDate` + `ParseTimeframe`）

```go
// 自然语言时间窗 → Duration，检索层按 updated_at 过滤
if d, err := memory.ParseTimeframe("7d"); err == nil {
	t := time.Now().Add(-d)
	opts.AfterDate = &t
}
// 支持："7d"/"24h"/"2 days ago"/"last week"/"last month"/"today"/"yesterday"
// 中文单位："7天"/"3小时" 等；无法解析返回错误
```

### Metadata 过滤（`MetadataFilters`）

Go 侧后过滤（避免 SQL JSON 兼容问题），字段→值精确匹配，全部命中才保留：

```go
opts.MetadataFilters = map[string]string{
	"permalink": "memdir/user-比价目标.md", // 内置字段：file_path/session_key/description/permalink
	"project":   "lenovo",                  // 其余键匹配 EntityMetadata（自定义 frontmatter 字段）
}
```

注意：过滤发生在检索后（LIMIT 之后），过滤后结果数可能少于 `Limit`；需要精确命中场景请适当放大 `Limit`。

### Relation 过滤（`RelationType` / `RelationTarget`）

按结构化关系反查「谁链接到我」：

```go
// 只召回声明了 implements 出边关系的记忆
opts.RelationType = "implements"

// 只召回链接指向 design-spec 的记忆（自动消歧：wiki-link 原文 / permalink / name）
opts.RelationTarget = "design-spec"
```

### 检索追踪（`SearchTrace`）

```go
trace := &memory.SearchTrace{}
opts.Trace = trace
results, _ := mgr.Search(ctx, query, opts)
// trace.Mode             检索模式：fts / like / vector / hybrid
// trace.RelaxedTriggered strict FTS 零命中后是否触发了 relaxed 回退
// trace.LikeFallback     短查询（<3 字符）是否走了 LIKE 子串回退
// trace.Hits / trace.Duration  原始命中数与 store 检索耗时（bytedp 接 Langfuse 用）
```

### 引用（Citations）

```go
opts.IncludeCitations = true
// r.Citation 格式："path/to/file.md#L10-L20"；无文件路径时用 SessionKey，再退化为 ID
```

### 便捷检索方法

```go
mgr.SearchByKeyword(ctx, "关键词", limit)              // FTS5 关键词
mgr.SearchBySource(ctx, memory.MemorySourceLongTerm)  // 按来源
mgr.SearchByType(ctx, memory.MemoryTypeUser)          // 按类型
mgr.SearchByTag(ctx, "标签")                            // 按标签
mgr.SearchByText(ctx, "文本")                           // 文本子串
```

## 关系图谱

`relations` 表是关系行的结构化投影（`UNIQUE(file_path, relation_type, target, context)`）。`Indexer` 同步文件时自动写入并全量重建；手工写入场景可直接调底层 store：

```go
sqliteStore := store // *SQLiteStore

// 结构入库（upsert，同一 (file, type, target, context) 只保留一行）
err := sqliteStore.SaveRelations("/memdir/a.md", "a", []memory.RelationRecord{
	{Type: "implements", Target: "design-spec"},
	{Type: "relates_to", Target: "user-goal", Context: "登录后使用"},
}, mtimeMs)

// 按文件清理出边关系（文件删除时调用）
err = sqliteStore.DeleteRelationsByFile("/memdir/a.md")

// 查询（MemoryManager 包装）
incoming, _ := mgr.ListIncomingRelations(ctx, "design-spec") // 谁链接到我（自动消歧 permalink/name）
outgoing, _ := mgr.ListOutgoingRelations(ctx, "/memdir/a.md") // 我链接到谁
```

检索过滤用 `SearchOptions.RelationType` / `RelationTarget`（见上）。target 消歧依赖元数据：`Indexer` 会把 frontmatter 的 `name` 与 `permalink` 写入 `entity_metadata` JSON，使「按标题写链接」也能匹配「按 permalink 落库」的记忆。

## 实体图谱

`entities` 表（`UNIQUE(name)`）把关系行的 `[[Target]]` 链接目标与门禁提取的实体清单汇聚成图节点，是关系图谱之上的派生层：实体可由关系行与门禁输出随时重建，不随文件删除级联清理（显式删除用 `DeleteEntity`）。

```go
type EntityRecord struct {
	Name       string // 实体名（全局唯一键）
	EntityType string // LLM 通道可填：person/project/tool/…
	Summary    string // LLM 通道可填，实体摘要
	Source     string // "link" | "llm"，来源通道
	MtimeMs    int64
}
```

### 双通道 upsert 语义

- **link 通道**（`Indexer` 同步时自动建）：关系行目标 + 文件 frontmatter 的 name/permalink 都会 upsert 为实体（`Source="link"`）。`ON CONFLICT(name) DO NOTHING`——仅确保节点存在，绝不覆盖更丰富的数据。
- **llm 通道**（适配层门禁提取）：`Source="llm"`，`ON CONFLICT(name) DO UPDATE`——非空字段覆盖（`entity_type`/`summary`），空字段保留原值，并把 source 升级为 `llm`。

### 实体 CRUD 与实体页查询

```go
// MemoryManager 包装（LLM 适配层可直接使用）
mgr.UpsertEntities(ctx, entities, time.Now().UnixMilli()) // 双通道 upsert（见上）
entities, _ := mgr.ListEntities(ctx, 50)                  // 按 mtime 倒序
entity, _ := mgr.GetEntity(ctx, "design-spec")            // 精确名查一个（nil = 不存在）
err := mgr.DeleteEntity(ctx, "design-spec")               // 显式删除，不做级联

// 实体页：该实体相关的关系边
incoming, _ := mgr.ListEntityRelations(ctx, "design-spec", true)  // 谁指向我（入边，自动消歧）
outgoing, _ := mgr.ListEntityRelations(ctx, "a", false)           // 我声明的关系（按 source_permalink 匹配）
```

### 图遍历 Traverse

从实体名出发 BFS 展开 N 跳关系边集（边去重，入边向上游 source 走、出边向下游 target 走）：

```go
edges, _ := mgr.TraverseRelations(ctx, "design-spec", 2) // 双向 2 跳（hops<=0 默认 1）
// 底层可选 outgoingOnly（只沿出边下游走）：
// sqliteStore.Traverse("a", 2, true)
```

### 模糊消歧

`ListIncomingRelations` / `ListEntityRelations` / `Traverse` 的目标匹配顺序：精确 alias（entity_metadata 的 name/permalink 全等）→ 未命中时回退 entities 表候选（前缀匹配优先、子串次之，最多 5 个）。entities 表规模远小于 memories，全表扫描后排序即可，无需额外索引。

## 治理与运维

### TTL 自动清理

```go
storeCfg := memory.DefaultStoreConfig("./memory.db", provider)
storeCfg.TTLConfig = memory.TTLConfig{
	Enabled:           true,
	RetentionDays:     90,               // 保留天数，0 = 不限制
	CleanupInterval:   24 * time.Hour,   // 后台清理周期
	PreserveImportant: true,             // importance >= 0.8 的记忆永不清理
}
store, _ := memory.NewSQLiteStore(storeCfg)
// 创建后自动启动后台清理 goroutine，StopCleanup() 可停止
```

### 手动清理与统计

```go
mgr.PurgeOldMemories(30)                                  // 删除 30 天前的记忆，返回删除数
mgr.PurgeBySource(memory.MemorySourceDaily, 30)           // 按来源清理
stats, _ := mgr.GetStats(ctx)                             // TotalCount / CacheSize
storage, _ := mgr.GetStorageStats()                       // 表大小等字节统计
```

### LRU 缓存

`MemoryManager` 内置 LRU 缓存（默认 1000 条），`ManagerConfig.CacheMaxSize` 可调；也可独立使用：

```go
cache := memory.NewLRUCache(1000)
cache.Put(key, ve)
ve, ok := cache.Get(key)
cache.SetOnEvict(func(key string, value *memory.VectorEmbedding) { /* 逐出回调 */ })
```

## memdir 包：三类型长期记忆文件层

`github.com/iqyeah/bytedp_memory/memdir` 实现「文件为真源、SQLite 为索引」的长期记忆层。每条记忆是一个带 frontmatter 的 Markdown 文件：

```markdown
---
name: 登录门户
description: 联想商城登录入口
type: reference
---

正文：登录地址、入口按钮位置等。
```

### 类型 `MemoryKind`

`KindUser` / `KindFeedback` / `KindReference`（`ParseMemoryKind` 解析非法值返回空）。

### 扫描与清单

```go
files, err := memdir.ScanMemoryFiles("/workspace/memory/memdir")
// 递归扫 .md（排除 MEMORY.md），mtime 倒序，上限 200 条
// 每条：Filename / FilePath / MtimeMs / Description / Kind

manifest := memdir.FormatMemoryManifest(files, memdir.MemoryAgeLabel)
// 输出：- [user] user-goal.md (3 days ago): 用户目标描述
```

### 索引截断与指针行

```go
res := memdir.TruncateEntrypoint(indexContent)
// 200 行 / 25KB 双上限，超限追加 WARNING；res.Content / res.WasTruncated / res.Reason

line := memdir.FormatPointerLine("登录门户", "memdir/reference-login.md", "联想商城登录入口")
// 输出：- [登录门户](memdir/reference-login.md) — 联想商城登录入口（≤150 字符）
```

### 新鲜度

```go
memdir.MemoryAgeLabel(mtimeMs)   // "today" / "yesterday" / "N days ago"
memdir.FreshnessWarning(mtimeMs) // >1 天返回"时点快照、用前验证"警告，否则空
```

### 观察行与 permalink

观察行（`- [category] content #tags`）是记忆文件内的结构化事实行，可单独索引为一条记忆（行级召回）：

```go
obs := memdir.ParseObservations(content)
// 宽松模式：不要求 heading；排除 checkbox（[ ]/[x]）、Markdown 链接（[t](url)）、wiki-link（[[）
// 每条：Category / Content / Tags / LineNumber（1-based）
```

稳定 permalink（Claude Code memdir 兼容）：frontmatter 自定义 `permalink` 字段，缺省回退文件名：

```markdown
---
name: 用户目标
permalink: user-goal
---
```

```go
fm := memdir.ParseFrontmatter(content, memdir.FrontmatterMaxLines)
p := memdir.ResolvePermalink(fm, "memdir/user-目标.md") // "user-goal"；缺省时返回文件名
```

关系行解析（wiki-link）：

```go
rels := memdir.ParseRelations(content)
// 三种语法：bare [[X]]、type [[X]] (context)、行内 links_to [[X]]
// 每条：Type / Target / Context / LineNumber
```

### 行为规范提示词

`memdir` 提供可直接注入 LLM 提示词的常量（源自 Claude Code 记忆体系）：

- `memdir.TypesSection`：三类型 when_to_save / how_to_use / body_structure 规范
- `memdir.WhatNotToSaveSection`：什么不该存（任务日志、代码可推导的事实等）
- `memdir.HowToSaveSection`：两步保存（写文件 + 更新索引指针）
- `memdir.WhenToAccessSection` / `memdir.TrustingRecallSection`：何时读取、用前验证
- `memdir.MemoryFrontmatterExample` / `memdir.MemoryOtherPersistenceSection`

## sqlite-vec 向量能力（已可用）

向量搜索由 **CGo-free 的 sqlite-vec 打包包**提供：`modernc.org/sqlite/vec`（v0.1.9，暴力 KNN）。模块在 `store.go` blank import 了 `modernc.org/sqlite` + `modernc.org/sqlite/vec`，后者通过 `sqlite3_auto_extension` 在每个连接上自动注册 `vec0` 虚拟表与 `vec_*` 函数，无需 `load_extension`、无需 C 编译环境，交叉编译友好。

- 向量存储与查询继续使用既有链路：`initVectorSearch` 建 `vec0` 表（`id TEXT PRIMARY KEY, embedding FLOAT[n]`）→ `insertVector` 写入 JSON 数组 → `searchVector` 用 `embedding MATCH ? AND k = ?`（JOIN 查询必须带 k 约束）取 KNN 后过滤。
- **注意**：vec0 虚拟表不支持 UPSERT，写入为 delete + insert（`insertVector` 内部已处理）。
- 打包的 sqlite-vec 为 v0.1.x 暴力 KNN（无 ANN 索引），万级~10 万级向量规模适用；超大规模需另选引擎。
- 引入 vec 后 `RegisterPageCache` 不可用（auto-extension 初始化会拒绝页缓存配置）。

## 常见陷阱

1. **provider 必填**：`NewMemoryManager` 要求 `Provider` 非 nil；未配置 embedding 时只能走 `TextQuery` 纯 FTS 路径。
2. **`Types` 为空 = 全部类型**：默认过滤串已包含新三类型（user/feedback/reference），指定 `Types` 后仅搜指定类型。
3. **`AddMemory` 不自动分块**：长文本直接整段 embedding 会稀释语义，先 `ChunkMarkdown`（或 `ChunkText`）再逐块写入。
4. **观察行幂等依赖 `ObservationKey`**：带非空 `ObservationKey` 的 `AddMemory` 走 `ON CONFLICT(observation_key) DO NOTHING`，同 key 重复写入静默跳过（不产生重复行、不写向量/FTS）；**观察键按 category+content 哈希全局唯一**，不同文件中的相同观察行只会索引一条。
5. **空 `ObservationKey` 绑定 NULL**：Go 空字符串绑定为 SQL NULL（而非 `''`），普通行不受唯一索引约束，永不误去重。
6. **短查询自动 LIKE 回退**：trigram 分词对 3 字符以下查询无法构建 MATCH，检索层已内置 relaxed（OR 展开/前缀）→ LIKE 两级回退，`Trace` 可观测是否触发；调用方无需自行回退。
7. **MinScore 语义**：向量相似度与 FTS rank 均被归一化为 0-1 相似度（1/(1+distance)）；`DefaultSearchOptions()` 默认 `MinScore: 0.7`，低召回场景建议手动降至 0.5。
8. **`SessionKeys` 的放行语义**：全局记忆（NULL session_key）不受过滤影响，只隔离会话记忆——不要把全局记忆误带 SessionKey。
9. **`MetadataFilters` 是后过滤**：作用于 LIMIT 之后，过滤后可能不足 `Limit` 条；且仅对从 store 扫描出的元数据字段生效（旧数据无 `EntityMetadata` 时匹配为空）。
10. **并发**：`SQLiteStore` 内部使用带超时的读写锁（`TimeoutRWMutex`，默认 1s），单连接池（`SetMaxOpenConns(1)`）适配 SQLite 文件锁。
11. **向量搜索依赖 vec0**：`EnableVectorSearch` 开启且 `modernc.org/sqlite/vec` 已打包（模块内建），无需外部扩展文件；初始化失败自动降级为 FTS 并打 Warning，不影响其他能力。注意引入 vec 后 `RegisterPageCache` 不可用。
12. **布尔查询白名单**：FTS5 查询中 `AND/OR/NOT` 与括号放行，`*+-^~:=<>` 等符号被剥离，NEAR 等不支持运算符按普通词处理；中文短语请用引号包裹。
13. **Relation 过滤依赖 relations 表**：该表由 `Indexer` 同步 memdir 文件时自动写入；手工 `AddMemory` 写入的记忆需自行调 `store.SaveRelations` 才有关系可过滤、可反查。

## 工具函数附录

| 函数 | 说明 |
|---|---|
| `memory.ChunkText(text, maxTokens)` | 按 token 估算分块（兼容保留） |
| `memory.ChunkMarkdown(text, maxChars, overlap)` | Markdown 感知分块（标题边界 + 行边界 + 超长行字符窗口） |
| `memory.ParseTimeframe("7d")` | 自然语言时间窗解析为 `time.Duration` |
| `memory.ApplyMMR(results, cfg)` | 独立 MMR 重排 |
| `memory.ApplyTemporalDecay(results, cfg)` | 独立时间衰减 |
| `memory.NewLRUCache / NewTimeoutRWMutex` | 缓存 / 超时锁 |
| `memory.NewIndexer(mgr, dir, opts...)` | memdir 文件 → SQLite 增量同步器（`SyncFile` / `SyncAll`） |

## License

本模块自身 MIT。内置依赖许可：SQLite 本体公共领域；`modernc.org/sqlite` 模块代码 BSD-3-Clause；打包的 sqlite-vec v0.1.9（Copyright (c) 2024 Alex Garcia）为 MIT（上游 Apache-2.0 OR MIT 双许可，此处采用 MIT），其声明随 `modernc.org/sqlite` 模块根的 `LICENSE-SQLITE_VEC` 传播，再分发时需保留。
