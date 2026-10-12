# NOTE-FORMAT（记忆文件格式规范）

本模块长期记忆文件（`memory/memdir/*.md`）的语法规范。文件是真源（source of
truth）：人可读、可编辑、可 git 版本化；SQLite 向量/FTS 索引只是其派生投影，
由 `Indexer` 增量重建。

一条记忆文件由三部分组成：YAML frontmatter、正文（观察行）、关系行。

```markdown
---
name: Coffee Brewing Methods
description: one-line relevance hook
type: user
permalink: coffee-brewing-methods
status: active
---

# Coffee Brewing Methods

## Observations
- [preference] Pour over provides more flavor clarity than French press #brewing
- [technique] Water temperature at 205°F extracts optimal compounds (based on OWASP audit)

## Relations
- relates_to [[Coffee Bean Origins]]
- requires [[Proper Grinding Technique]]
```

## Frontmatter

`---` 定界，字段必须单行。标准字段：

| 字段 | 说明 |
|---|---|
| `name` | 记忆标题 |
| `description` | 单行相关性钩子，用于未来召回时的相关性判断 |
| `type` | 记忆类型：`user` / `feedback` / `reference` |
| `permalink` | 可选稳定标识；缺省回退到文件名 |

**自定义字段**：不在标准集合内的任意 key 会被收集进 `entity_metadata`，供
结构化检索过滤使用（`MetadataFilters`）。

```yaml
---
name: Paul Graham
type: user
status: active
source: wikipedia
---
```

这里 `status`、`source` 进入 `entity_metadata`。

解析边界：未闭合的 frontmatter 整体忽略；仅检查前 `FrontmatterMaxLines`(30)
行；`type` 缺失或不合法时该文件仍可用（降级）。

## Observations（观察行）

观察是一条带分类的事实，写成 Markdown 列表项：

```
- [category] content #tag1 #tag2 (context)
```

| 部分 | 必填 | 说明 |
|---|---|---|
| `[category]` | 是 | 方括号内的分类，除 `[]()` 外任意文本 |
| content | 是 | 事实内容 |
| `#tags` | 否 | 行内标签 |
| `(context)` | 否 | 行尾括号内文字，剥离为 `Observation.Context` |

**非观察**（解析器排除）：复选框（`- [ ]` / `- [x]`）、Markdown 链接
（`- [text](url)`）、裸 wiki 链接（`- [[Target]]`，属于关系）、无 `[category]`
的普通列表项。

`(context)` 只在开括号前有空白（或位于行首）时被视为 context，函数调用
`Foo()` 不受影响。

## Relations（关系行）

关系用 wiki 链接连接记忆，形成知识图。三种形式：

| 形式 | 示例 | 解析出的类型 |
|---|---|---|
| 显式 | `- implements [[Search Design]]` | `implements` |
| 显式带 context | `- depends_on [[Database Schema]] (co-founder)` | `depends_on` + context |
| 裸链接 | `- [[Some Entity]]` | `relates_to` |
| 行内引用 | 正文中的 `[[Core Design]]` | `links_to` |

`[[Target]]` 目标按标题或 permalink 匹配；`relates_to` / `links_to` 为约定，
任意文本都可作为关系类型。

## 与 basic-memory 的关系

本格式是 [basic-memory](https://github.com/basicmachines-co/basic-memory)
NOTE-FORMAT 的收敛子集：保留「文件为真源」「观察行 `[category] ... (context)`」
「关系 `type [[Target]]`」「自定义字段 → entity_metadata」的核心语义，暂不引入
Picoschema 校验、`memory://` URL 与 `@effective[...]` 时间断言。
