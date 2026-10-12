# bytedp_memory 能力评估与待提升路线图

> 生成日期：2026-10-12。评估基准：Claude Code memdir 记忆生态、[basic-memory](https://github.com/basicmachines-co/basic-memory)（Python 服务端产品，src 约 12.8 万行）。
> 本文档是能力差距与演进路线的一站式清单，随交付进度滚动更新。

## 一、三方对比总评

| 维度 | Claude Code memdir 生态 | basic-memory | bytedp_memory | 结论 |
|---|---|---|---|---|
| 写入模型 | Agent 直接写 Markdown，无索引层 | 文件真源 + watcher 监听 + 分块索引 | 文件真源 + 指纹 diff 增量索引（Indexer） | bytedp 更省 embedding，优于后两者 |
| 检索 | 无（靠 LLM 读文件 / 全量注入） | 语义 + FTS + 混合 + rerank | FTS5 trigram 三级回退 + 混合 + MMR + 时间衰减 | bytedp ≥ basic-memory，远超 Claude Code |
| 图谱 | 无（仅 wiki-link 文本行） | Entity/Observation/Relation 三实体 + 关系自动生成 + 图查询 | Entity 双通道建模 + Relation 双向查询 + BFS 图遍历 + 模糊消歧 | 接近 basic-memory，远超 Claude Code |
| 结构化行 | 观察行文本约定 | Observation 入库去重 | 观察行幂等 upsert（ObservationKey 唯一索引）+ 行级召回 | bytedp 持平且更轻 |
| 服务面 | Agent 内建 | FastAPI + MCP 16 工具 + CLI 30+ 命令 | 无（嵌入式库） | basic-memory 完胜，但属定位差异 |
| 依赖面 | 零依赖 | Python 全家桶（数百依赖） | 3 个直接依赖，零 LLM 依赖 | bytedp 最优 |

**总评**：
- **检索内核**：优秀。FTS5 trigram 的 CJK 三级回退、hybrid 融合、MMR、指纹增量索引、行级召回这条链路已达到甚至超过 basic-memory 对应子功能（其"语义分块"实为字符窗口切分，bytedp 的 ChunkMarkdown 更优）。
- **知识图谱**：已接近 basic-memory。Relation 结构化双向查询、Entity 实体建模（link/llm 双通道）、BFS 图遍历、target 模糊消歧、关系自动生成均已落地；剩余差距仅是 basic-memory 的别名解析/实体合并等重引擎能力（C 组不做）。
- **工程简洁性**：优秀。纯 Go 单库、零 LLM 依赖、3 个直接依赖。
- **服务/生态面**：空白（刻意定位，主项目 agent 层已自建消费面）。

## 二、当前优势（已交付能力）

检索与写入链路的完整清单：

1. **FTS5 trigram 三级回退**：strict MATCH（布尔白名单 sanitize）→ relaxed（OR 展开/前缀）→ LIKE 子串（<3 rune），CJK 感知
2. **混合检索**：向量 + FTS 双通道融合（默认 0.7/0.3 权重）
3. **后处理管线**：metadata 过滤 → 时间衰减 → MMR → MinScore → Limit（固定顺序）
4. **指纹 diff 增量索引**：ChunkMarkdown 分块 + SHA-256 内容指纹，未变化 chunk 不重新 embedding，消失行自动清理
5. **观察行幂等 upsert**：`- [category] content` 行级索引，ObservationKey 唯一索引 + 空键绑定 NULL 防误去重
6. **行级召回注入**：观察行命中注入 `file#Lxx` 行级定位与行文本，替代整文件注入
7. **结构化关系**：relations 表 + SaveRelations/ListIncoming/ListOutgoing + RelationType/RelationTarget 检索过滤 + name/permalink 消歧
8. **实体图谱**：entities 表（UNIQUE(name)）+ EntityRecord + 双通道 upsert（link 自动建不降级 / llm 富化覆盖）+ 实体 CRUD + 实体页关系查询（ListEntityRelations）
9. **图遍历**：Traverse BFS N 跳展开（出边下游 / 入边上游，边去重，outgoingOnly 可选）+ TraverseRelations 包装
10. **模糊消歧**：精确 alias 未命中回退 entities 表前缀/子串候选（排序 + cap 5）
11. **关系自动生成**：门禁提取提示词追加 relations/entities 输出，关系行渲染为 `- type [[target]] (context)` 落文件由 Indexer 自动入库，实体清单直写 entities 表
12. **版本化 schema 迁移**：schema_version 表 + 有序迁移序列（v1 memories / v2 relations / v3 entities），老库幂等收敛
13. **过滤体系**：Sources/Types/SessionKeys（NULL 放行语义）/AfterDate（ParseTimeframe 自然语言时间窗）/MetadataFilters（EntityMetadata 精确匹配）
14. **检索追踪**：SearchTrace（Mode/RelaxedTriggered/LikeFallback/Hits/Duration），对接 Langfuse
15. **治理**：TTL 自动清理（Importance≥0.8 豁免）、Purge、存储统计、LRU 缓存、超时读写锁
16. **memdir 兼容**：三类型（user/feedback/reference）、frontmatter（name/description/type/permalink）、MEMORY.md 指针、新鲜度标注、LLM 提示词常量

## 三、待提升清单

### P1：能力缺口（建议做，按价值排序）

#### P1-1 Entity 实体建模与实体索引（1~1.5 天）✅ 已交付（2026-10-12）

- **缺口**：relations 的 target 是字符串；没有独立的"实体"概念。basic-memory 的 Entity/Observation/Relation 三实体中，Entity 是其核心差异化——实体内聚观察行与关系，支持实体页查询。
- **目标**：新增 `entities` 表（name 唯一 / entity_type / summary / source 双通道 / mtime_ms），关系行目标与文件 name/permalink 自动建实体（link 通道），LLM 门禁补充实体清单（llm 通道），支持实体 CRUD 与实体页关系查询（`ListEntityRelations` 入/出边）；观察行不挂实体（控制范围）。
- **涉及文件**：`schema.go`（DDL）、新建 `entity_store.go`、`indexer.go`（实体写入）、`types.go`（EntityRecord）、`search.go`（MemoryManager 查询 API）。

#### P1-2 关系自动生成（0.5 天）✅ 已交付（2026-10-12）

- **缺口**：关系行靠人工写 `- type [[X]]`；basic-memory 有 LLM 自动生成关系（alembic 迁移 r1m2n3o4p5q6 专门支持）。
- **目标**：门禁提取提示词追加"顺带生成 relates_to/contrasts_with 关系行"，写入 memdir 文件后由 Indexer 自动双写 relations 表（链路已通，只差生成端）。
- **涉及文件**：主项目 `agent/memory_extract.go` 提示词（模块无改动）。

#### P1-3 图遍历 / 多跳展开（0.5 天）✅ 已交付（2026-10-12）

- **缺口**：ListIncoming/ListOutgoing 只有一跳；basic-memory 的 relation_repository 支持图遍历供 build_context 展开上下文。
- **目标**：`Traverse(ctx, start, hops, opts)`——从实体/文件出发展开 N 跳关系子图，返回去重后的节点与边。
- **涉及文件**：`relation_store.go`（BFS）、`search.go`（包装）。

#### P1-4 远程 rerank 精排（1~1.5 天）

- **缺口**：召回 topN 后无精排；basic-memory 有 fastembed_rerank_provider（本地 cross-encoder）。
- **目标**：召回后可选 rerank——远程 API（如 DashScope rerank 模型）或轻量启发式（关键词覆盖率 + 新鲜度加权），返回精排 topK。仅当检索质量成为瓶颈时启动。
- **涉及文件**：新建 `rerank.go`、`types.go`（RerankConfig）、`search.go`（管线接入点：MMR 之后）。

#### P1-5 target 模糊匹配消歧（0.5 天）✅ 已交付（2026-10-12）

- **缺口**：`resolveRelationTarget` 当前精确匹配 name/permalink；basic-memory 用 pg_trgm 模糊消歧（笔误/近义词也能命中）。
- **目标**：精确匹配未命中时回退 entities 表候选（前缀优先/子串次之，排序后 cap 5），返回候选。
- **涉及文件**：`relation_store.go`（resolveRelationTarget）。

### P2：工程/运维面（按需做）

#### P2-1 版本化 schema 迁移（0.5 天）✅ 已交付（2026-10-12）

- **缺口**：DDL 是 `CREATE IF NOT EXISTS` 平铺；basic-memory 用 Alembic 版本化迁移。
- **目标**：`schema_version` 表 + 有序迁移列表，启动时逐版本执行。
- **涉及文件**：`schema.go`。

#### P2-2 文件 rename 追踪（0.5 天）

- **缺口**：改名 = 删旧 + 重建索引（重新 embedding）；当前 SyncAll 按文件消失清理。
- **目标**：基于 permalink 识别"旧 permalink 消失 + 新 permalink 出现"，迁移行元数据而非重建。
- **涉及文件**：`indexer.go`。

#### P2-3 文件 watcher 自动监听（0.5 天，低优先）

- **缺口**：库层面无自动监听；basic-memory 用 watchdog。
- **目标**：可选 fsnotify 监听，文件变化自动 SyncFile。
- **备注**：主项目 agent 层已有明确调用点（turn 结束同步），此能力优先级低。
- **涉及文件**：新建 `watcher.go`（可选依赖）。

#### P2-4 对话导入器（0.5~1 天，低优先）

- **缺口**：basic-memory 支持 ChatGPT/Claude 导出对话导入。
- **目标**：JSON 导出 → 门禁提取 → memdir 文件。属定位缺口，主项目不需要可跳过。
- **涉及文件**：主项目 agent 层或独立小工具。

### P3：明确不做（定位性缺口）

| 项 | 原因 |
|---|---|
| MCP server / HTTP API / CLI | 定位差异：模块是嵌入式检索内核，主项目 agent 层与网关已自建消费面 |
| 多后端（Postgres/Milvus） | 违背纯 Go 单库定位，SQLite 单文件分发是核心卖点 |
| 完整知识图谱（实体 UI、图存储引擎） | P1-1 + P1-3 已覆盖 build_context 需求，重引擎不划算 |
| Redis read-through 缓存 | 进程内 LRU 对单进程 Agent 足够 |
| 语义分块 | basic-memory 的"语义分块"实为字符窗口切分，bytedp 的 ChunkMarkdown（标题边界+行边界+超长行窗口）同层且更优 |
| 插件生态 | 与模块定位无关 |

## 四、执行建议顺序

1. ~~**P1-1 Entity 建模** → **P1-2 关系自动生成**~~ ✅ 已交付（含 P1-3 图遍历、P1-5 模糊消歧、P2-1 版本化迁移）
2. **P1-4 rerank**：仅当实际使用中检索精度成为瓶颈时启动
3. **P2-2/P2-3/P2-4**：开源发布前做 P2-2（rename 追踪）；其余按需

## 五、一句话现状

作为嵌入式检索内核，bytedp_memory 已是同类最优解之一（检索链路、增量索引、依赖面），Entity 建模与图谱深度（双通道实体、图遍历、关系自动生成）也已补齐；剩余差距仅是 basic-memory 的别名解析/实体合并等重引擎能力，属明确不做范围。
