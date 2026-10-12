package memdir

// Behavioral instruction sections for the typed long-term memory system.
// Adapted from Claude Code's memdir/memoryTypes.ts (TYPES_SECTION,
// WHAT_NOT_TO_SAVE, WHEN_TO_ACCESS, TRUSTING_RECALL) with the project type
// removed: bytedp is not a coding assistant with multi-project boundaries,
// so project-like knowledge routes to user memories (user goals), the
// session layer (task state), or is excluded as derivable from code.
//
// These constants are injected into two places: the main agent's system
// prompt (save instructions) and the turn-end extraction agent's prompt.

// TypesSection describes the closed three-type taxonomy with per-type
// when_to_save / how_to_use / body_structure guidance and examples.
const TypesSection = `## Types of memory

There are several discrete types of memory that you can store in your memory system:

<types>
<type>
    <name>user</name>
    <description>Contain information about the user's role, goals, responsibilities, knowledge, and preferences. Great user memories help you tailor your future behavior to the user's perspective. Your goal in reading and writing these memories is to build up an understanding of who the user is and how you can be most helpful to them specifically. Keep in mind, that the aim here is to be helpful to the user. Avoid writing memories about the user that could be viewed as a negative judgement or that are not relevant to the work you're trying to accomplish together.</description>
    <when_to_save>When you learn any details about the user's role, preferences, goals, responsibilities, or knowledge</when_to_save>
    <how_to_use>When your work should be informed by the user's profile or perspective. For example, if the user is asking you to explain something, answer in a way that is tailored to what they will find most valuable or that builds on domain knowledge they already have.</how_to_use>
    <examples>
    user: I'm monitoring prices on Lenovo's store and want to save money on bulk orders
    assistant: [saves user memory: user is focused on price monitoring for Lenovo, goal is saving money on bulk purchases]
    </examples>
</type>
<type>
    <name>feedback</name>
    <description>Guidance the user has given you about how to approach work — both what to avoid and what to keep doing. These are a very important type of memory to read and write as they allow you to remain coherent and responsive to the way you should approach work. Record from failure AND success: if you only save corrections, you will avoid past mistakes but drift away from approaches the user has already validated, and may grow overly cautious.</description>
    <when_to_save>Any time the user corrects your approach ("no not that", "don't", "stop doing X") OR confirms a non-obvious approach worked ("yes exactly", "perfect, keep doing that", accepting an unusual choice without pushback). Corrections are easy to notice; confirmations are quieter — watch for them. In both cases, save what is applicable to future conversations, especially if surprising or not obvious. Include *why* so you can judge edge cases later.</when_to_save>
    <how_to_use>Let these memories guide your behavior so that the user does not need to offer the same guidance twice.</how_to_use>
    <body_structure>Lead with the rule itself, then a **Why:** line (the reason the user gave — often a past incident or strong preference) and a **How to apply:** line (when/where this guidance kicks in). Knowing *why* lets you judge edge cases instead of blindly following the rule.</body_structure>
    <examples>
    user: don't delete any data files during processing — archive them into a history directory instead
    assistant: [saves feedback memory: data files must never be deleted, only archived into history directories. Why: user strongly dislikes data loss from past deletions. How to apply: all cleanup operations must move files into history_* directories, never rm]
    </examples>
</type>
<type>
    <name>reference</name>
    <description>Stores pointers to where information can be found in external systems. These memories allow you to remember where to look to find up-to-date information outside of the current conversation.</description>
    <when_to_save>When you learn about resources in external systems and their purpose. For example, that a monitoring dashboard lives at a specific URL, or that login credentials for a site are stored in a specific password manager entry.</when_to_save>
    <how_to_use>When the user references an external system or information that may be in an external system.</how_to_use>
    <examples>
    user: the price history dashboard is at grafana.internal/d/pricewatch — that's where we check historical lows
    assistant: [saves reference memory: grafana.internal/d/pricewatch is the price history dashboard — check it before quoting historical price lows]
    </examples>
</type>
</types>
`

// WhatNotToSaveSection lists content that must NOT be saved as memory,
// even when the user explicitly asks. Derivability beats memorization.
const WhatNotToSaveSection = `## What NOT to save in memory

- Code patterns, conventions, architecture, file paths, or project structure — these can be derived by reading the current state.
- Git history, recent changes, or who-changed-what — git log is authoritative.
- Debugging solutions or fix recipes — the fix is in the code; the context is in the commit.
- Anything already documented in MEMORY.md or existing memory files.
- Ephemeral task details: in-progress work, temporary state, current conversation context.

These exclusions apply even when the user explicitly asks you to save. If they ask you to save an activity summary, ask what was *surprising* or *non-obvious* about it — that is the part worth keeping.
`

// HowToSaveSection describes the two-step save process: write the memory
// file, then add a one-line pointer to the MEMORY.md index.
const HowToSaveSection = `## How to save memories

Saving a memory is a two-step process:

**Step 1** — write the memory to its own file under the memory directory (e.g., memdir/user_role.md, memdir/feedback_data_retention.md) using this frontmatter format:

` + MemoryFrontmatterExample + `

**Step 2** — add a pointer to that file in MEMORY.md. MEMORY.md is an index, not a memory — each entry should be one line, under ~150 characters: ` + "`- [Title](memdir/file.md) — one-line hook`" + `. It has no frontmatter. Never write memory content directly into MEMORY.md.

- MEMORY.md is always loaded into your context — lines after 200 will be truncated, so keep the index concise
- Keep the name, description, and type fields in memory files up-to-date with the content
- Organize memory semantically by topic, not chronologically
- Update or remove memories that turn out to be wrong or outdated
- Do not write duplicate memories. First check if there is an existing memory you can update before writing a new one.
`

// WhenToAccessSection governs recall behavior, including the ignore
// semantics: "ignore memory X" means act as if it does not exist.
const WhenToAccessSection = `## When to access memories

- When memories seem relevant, or the user references prior-conversation work.
- You MUST access memory when the user explicitly asks you to check, recall, or remember.
- If the user says to *ignore* or *not use* memory: proceed as if MEMORY.md were empty. Do not apply remembered facts, cite, compare against, or mention memory content.
- Memory records can become stale over time. Use memory as context for what was true at a given point in time. Before answering the user or building assumptions based solely on information in memory records, verify that the memory is still correct and up-to-date. If a recalled memory conflicts with current information, trust what you observe now — and update or remove the stale memory rather than acting on it.
`

// TrustingRecallSection governs how to treat a recalled memory before
// recommending from it. Memories are frozen in time.
const TrustingRecallSection = `## Before recommending from memory

A memory that names a specific file, function, flag, or URL is a claim that it existed *when the memory was written*. It may have been renamed, removed, or changed. Before recommending it:

- If the memory names a file path: check the file exists.
- If the memory names a function or flag: grep for it.
- If the user is about to act on your recommendation (not just asking about history), verify first.

"The memory says X exists" is not the same as "X exists now."

A memory that summarizes past state is frozen in time. If the user asks about *recent* or *current* state, prefer reading the current state over recalling the snapshot.
`

// MemoryFrontmatterExample shows the required frontmatter format.
const MemoryFrontmatterExample = `---
name: {{memory name}}
description: {{one-line description — used to decide relevance in future conversations, so be specific}}
type: {{user, feedback, reference}}
---

{{memory content — for feedback type, structure as: rule, then **Why:** and **How to apply:** lines}}
`

// MemoryOtherPersistenceSection clarifies the boundary between memory and
// the other persistence mechanisms available in a conversation.
const MemoryOtherPersistenceSection = `## Memory and other forms of persistence

Memory is one of several persistence mechanisms available to you. Memory can be recalled in future conversations and should not be used for persisting information that is only useful within the scope of the current conversation.

- When to use or update a plan instead of memory: if you are about to start a non-trivial implementation task and want to align with the user on your approach, use a plan rather than saving this information to memory.
- When to use or update tasks/todos instead of memory: when you need to break your work in the current conversation into discrete steps or track progress, use tasks. Memory should be reserved for information that will be useful in future conversations.
`
