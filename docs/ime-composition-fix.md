# IME 输入法组合问题修复记录

## 问题描述

### 现象
- **症状**：使用输入法（如中文、日文）输入时，搜索会在输入中间状态就触发
- **表现**：输入 "config.conf" 时，搜索结果显示的是 "config"（中间状态）而不是最终输入的 "config.conf"
- **原因**：搜索的防抖机制在 IME 组合输入（composition）过程中也会触发

### 用户反馈
```
"我发现搜索框录入的时候，随着输入法的输入，好像有时候结果会卡在未输入完整的地方
（没有搜索最终的输入结果），这个要解决下，比如我输入的config.conf，会发现最终搜索是config"

"实时去搜索没问题，但是要确保最终没有输入的时候，搜索的结果一定是最后输入的内容对应的结果"
```

## 问题原理

### IME 组合输入过程

当用户使用输入法输入时，会经历多个阶段：

```
输入 "config.conf" 的过程（使用中文输入法）：

1. compositionstart 事件 - 开始组合输入
2. 用户输入: c
   - input 事件触发，searchQuery = "c"
   - 但这只是中间状态，不是最终输入
3. 用户输入: o
   - input 事件触发，searchQuery = "co"
   - 仍然是中间状态
4. 用户输入: n, f, i, g
   - 多次 input 事件，searchQuery 不断更新
   - 都是中间状态
5. compositionend 事件 - 结束组合输入
   - 最终 searchQuery = "config"（如果用户选择了"config"）
   - 或 searchQuery = "config.conf"（如果用户继续输入）
```

### 原有问题

原有代码在 reactive 语句中直接响应 `searchQuery` 的变化：

```javascript
$: if (searchQuery !== undefined || useRegex !== undefined) {
  // 清除之前的定时器
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer)
  }

  // 设置新的定时器
  searchDebounceTimer = setTimeout(() => {
    performSearch()  // ❌ 会在组合输入过程中触发
  }, SEARCH_DEBOUNCE_DELAY)
}
```

**问题**：
- 每次 `searchQuery` 更新都会触发
- IME 组合输入过程中也会触发
- 搜索可能基于中间状态而不是最终输入

## 解决方案

### 1. 添加 IME 组合状态追踪

```javascript
// IME 输入法相关
let isComposing = false  // 是否正在输入法组合输入中
```

### 2. 添加组合事件处理器

```javascript
// IME 组合输入开始
function handleCompositionStart() {
  isComposing = true
}

// IME 组合输入结束
function handleCompositionEnd() {
  isComposing = false
  // 组合输入结束后，立即触发搜索（确保搜索最终输入的完整内容）
  // 清除可能存在的定时器
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer)
  }
  // 设置新的定时器执行搜索
  searchDebounceTimer = setTimeout(() => {
    performSearch()
  }, SEARCH_DEBOUNCE_DELAY)
}
```

### 3. 修改 reactive 语句逻辑

```javascript
// 监听输入变化和正则模式变化，实时搜索
// 搜索防抖：用户停止输入500ms后才执行搜索
$: if (searchQuery !== undefined || useRegex !== undefined) {
  // 清除之前的定时器
  if (searchDebounceTimer) {
    clearTimeout(searchDebounceTimer)
  }

  // 如果正在输入法组合输入中，不设置新的定时器
  if (!isComposing) {
    // 设置新的定时器
    searchDebounceTimer = setTimeout(() => {
      performSearch()
    }, SEARCH_DEBOUNCE_DELAY)
  }
}
```

### 4. 绑定事件到输入框

```svelte
<input
  type="text"
  class="search-input"
  placeholder="搜索文件..."
  bind:value={searchQuery}
  on:compositionstart={handleCompositionStart}
  on:compositionend={handleCompositionEnd}
  autofocus
  autocapitalize="off"
  autocorrect="off"
  spellcheck="false"
/>
```

## 工作流程

### 修复前（有问题）

```
用户输入: config.conf（使用中文输入法）

1. compositionstart
2. input: "c"       → 500ms 后搜索 "c"      ❌
3. input: "co"      → 500ms 后搜索 "co"     ❌
4. input: "conf"    → 500ms 后搜索 "conf"   ❌
5. input: "config"  → 500ms 后搜索 "config" ❌
6. compositionend
7. input: "config.conf" → 500ms 后搜索 "config.conf" ✅

结果：可能搜索到的是 "config" 而不是 "config.conf"
```

### 修复后（正确）

```
用户输入: config.conf（使用中文输入法）

1. compositionstart     → isComposing = true
2. input: "c"           → 不触发搜索（isComposing=true）
3. input: "co"          → 不触发搜索（isComposing=true）
4. input: "conf"        → 不触发搜索（isComposing=true）
5. input: "config"      → 不触发搜索（isComposing=true）
6. compositionend       → isComposing = false
   → 触发搜索定时器
7. （如果继续输入）
   input: ".conf"       → 触发新的搜索定时器
8. 500ms 后 → 搜索 "config.conf" ✅

结果：始终搜索最终完整的输入
```

## 技术细节

### IME 事件顺序

根据 W3C 规范，IME 事件的触发顺序是：

1. `compositionstart` - 组合开始
2. `compositionupdate` - 组合更新（可多次）
3. `input` - 输入变化（与 compositionupdate 交替）
4. `compositionend` - 组合结束

### 为什么使用 compositionend 触发搜索

```javascript
function handleCompositionEnd() {
  isComposing = false
  // 立即设置搜索定时器，确保搜索最终输入
  searchDebounceTimer = setTimeout(() => {
    performSearch()
  }, SEARCH_DEBOUNCE_DELAY)
}
```

**关键点**：
1. `compositionend` 后 `isComposing` 变为 `false`
2. 立即设置定时器，确保一定会搜索
3. 如果用户继续输入（如从 "config" 输入到 "config.conf"），reactive 语句会重新设置定时器
4. 最终搜索的一定是用户停止输入后的完整内容

### Svelte reactive 语句限制

注意：Svelte 的 reactive 语句（`$:`）中不能使用 `return` 语句

```javascript
// ❌ 错误写法
$: if (searchQuery !== undefined) {
  if (isComposing) {
    return  // SyntaxError: 'return' outside of function
  }
  performSearch()
}

// ✅ 正确写法
$: if (searchQuery !== undefined) {
  if (!isComposing) {
    performSearch()
  }
}
```

## 测试验证

### 测试场景

1. **场景1：中文输入法输入完整词组**
   - 输入：config.conf
   - 预期：搜索 "config.conf"
   - 结果：✅ 正确

2. **场景2：中文输入法输入后继续输入**
   - 输入：先输入 "config"，选择后继续输入 ".conf"
   - 预期：搜索 "config.conf"
   - 结果：✅ 正确

3. **场景3：直接英文输入（无 IME）**
   - 输入：config.conf
   - 预期：搜索 "config.conf"
   - 结果：✅ 正确

4. **场景4：快速输入**
   - 输入：快速打字 "test.txt"
   - 预期：搜索 "test.txt"
   - 结果：✅ 正确

## 相关资料

### W3C 标准
- [Composition Events](https://www.w3.org/TR/uievents/#events-compositionevents)
- [Input Events](https://www.w3.org/TR/input-events-2/)

### MDN 文档
- [compositionstart event](https://developer.mozilla.org/en-US/docs/Web/API/Element/compositionstart_event)
- [compositionend event](https://developer.mozilla.org/en-US/docs/Web/API/Element/compositionend_event)

### Svelte 文档
- [Reactive statements](https://svelte.dev/docs#component-format-script-3-$-marks-a-statement-as-reactive)
- [Event handlers](https://svelte.dev/docs#template-syntax-element-directives-on-eventname)

## 总结

这个问题是典型的**输入法组合事件处理问题**：

1. **问题本质**：搜索在 IME 组合输入的中间状态就触发了
2. **解决方案**：使用 `compositionstart` 和 `compositionend` 事件追踪组合状态
3. **关键机制**：
   - 组合输入期间（`isComposing=true`）不触发搜索
   - 组合结束后立即设置搜索定时器
   - 确保最终搜索的是用户完整的输入

**用户体验提升**：
- ✅ 不会在输入中间状态触发无意义的搜索
- ✅ 始终搜索用户最终输入的完整内容
- ✅ 保持了原有的防抖机制（500ms）
- ✅ 支持所有输入法（中文、日文、韩文等）

## 相关提交

- `[commit-hash]` - 修复 IME 输入法组合问题，确保搜索最终完整输入
