# Performance Optimization Plan: Transcript & Debug Tabs

## Problem Statement

The Transcript and Debug tabs in session detail are slow. Analysis reveals two main bottlenecks:

1. **No list virtualization** - All events render in a simple `.map()` loop, creating hundreds/thousands of DOM nodes
2. **Heavy Markdown rendering** - EventDetail uses react-markdown with KaTeX (math), lowlight (syntax highlighting), and remark-gfm, all re-rendering on every selection

## Current Implementation Issues

### TranscriptTab.tsx & DebugTab.tsx
- Lines 215-236 (Transcript) and 292-323 (Debug): Simple `.map()` without virtualization
- Every event creates a full DOM node even when not visible
- No React.memo on EventRow → parent re-renders cascade to all children
- DebugTab uses ref callbacks in a loop (lines 300-306) → expensive

### Markdown.tsx
- Uses `react-markdown` + `remark-gfm` + `remark-math` + `rehype-katex` + `lowlight`
- KaTeX CSS alone is heavy (~200KB)
- lowlight loads 7 language grammars (bash, json, yaml, python, ts, js, plaintext)
- Every event selection triggers full Markdown parse + render

### Package.json Bloat
- Both `highlight.js` AND `shiki` installed (redundant syntax highlighters)
- `streamdown` installed but not used in Markdown.tsx
- `katex` loaded unconditionally even when no math present

## Recommended Lightweight Replacements

### 1. List Virtualization (CRITICAL - Biggest Win)

**Add: `@tanstack/react-virtual`**
- Already in Tanstack ecosystem (you use @tanstack/react-table)
- Lightweight (~10KB), headless, flexible
- Supports dynamic row heights (EventRow varies based on content)
- Reduces DOM from 1000s of nodes to ~20-50 visible rows

**Why not react-window or react-virtuoso?**
- react-window: Fixed heights only (EventRow is dynamic)
- react-virtuoso: Heavier, more opinionated API
- @tanstack/react-virtual: Best fit for existing stack + dynamic heights

### 2. Markdown Rendering (HIGH IMPACT)

**Option A: Optimize Current Stack (Recommended)**
- Add `React.memo` to Markdown component
- Lazy-load KaTeX only when `$$` detected in text
- Replace lowlight with **prism.js** (~10KB vs highlight.js ~40KB)
- Cache parsed Markdown AST for repeated renders

**Option B: Replace with Lighter Alternative**
- **marked** (~15KB) - Fastest Markdown parser
- **markdown-it** (~30KB) - Extensible, good plugin ecosystem
- Use **shiki** (already installed) for syntax highlighting instead of lowlight
- Load KaTeX dynamically only when needed

**Option C: Use streamdown (Already Installed!)**
- package.json shows `streamdown` is installed
- Designed for streaming Markdown (perfect for chat interfaces)
- Likely optimized for incremental rendering
- Check if it supports math + syntax highlighting

### 3. Component-Level Optimizations

**EventRow.tsx**
```tsx
// Add React.memo to prevent cascade re-renders
export const EventRow = React.memo(function EventRow({ ... }) {
  // ... existing code
});
```

**TranscriptTab.tsx & DebugTab.tsx**
```tsx
// Replace .map() with virtualized list
import { useVirtualizer } from '@tanstack/react-virtual';

const virtualizer = useVirtualizer({
  count: displayEvents.length,
  getScrollElement: () => scrollRef.current,
  estimateSize: () => 60, // Estimate row height
  overscan: 5, // Render 5 extra rows above/below viewport
});

// Render only visible rows
<div ref={scrollRef} style={{ overflow: 'auto' }}>
  <div style={{ height: `${virtualizer.getTotalSize()}px` }}>
    {virtualizer.getVirtualItems().map((virtualRow) => {
      const de = displayEvents[virtualRow.index];
      return (
        <div key={de.primaryEvent.id} style={{ height: `${virtualRow.size}px` }}>
          <EventRow event={de.primaryEvent} ... />
        </div>
      );
    })}
  </div>
</div>
```

## Implementation Plan

### Phase 1: Add Virtualization (1-2 hours)
1. Install `@tanstack/react-virtual`
2. Update TranscriptTab.tsx to use virtualizer
3. Update DebugTab.tsx to use virtualizer
4. Add React.memo to EventRow component
5. Test scrolling performance with 1000+ events

**Expected improvement**: 10-50x faster initial render, smooth scrolling

### Phase 2: Optimize Markdown Rendering (2-3 hours)
1. Wrap Markdown component in React.memo
2. Implement lazy KaTeX loading:
   ```tsx
   const hasMath = /\$\$/.test(children);
   const rehypePlugins = hasMath ? [rehypeKatex] : [];
   ```
3. Replace lowlight with prism.js OR use shiki (already installed)
4. Add Markdown parse caching (memoize on input text)

**Expected improvement**: 2-5x faster detail panel renders

### Phase 3: Cleanup & Bundle Optimization (1 hour)
1. Remove unused syntax highlighter (keep either highlight.js OR shiki, not both)
2. Check if streamdown can replace react-markdown (if it supports math + highlighting)
3. Tree-shake unused highlight.js languages
4. Audit other heavy dependencies

**Expected improvement**: 20-30% smaller bundle size

## Alternative Lightweight Components

If you want to go even lighter, consider:

### For Lists:
- **@tanstack/react-virtual** (recommended) - 10KB, headless
- **react-window** - 6KB, simpler API but fixed heights
- **Clusterize.js** - Vanilla JS, framework-agnostic

### For Markdown:
- **marked** - 15KB, fastest parser
- **markdown-it** - 30KB, extensible
- **streamdown** - Already installed, streaming-optimized
- **mdx** - If you need JSX in Markdown (overkill here)

### For Syntax Highlighting:
- **prism.js** - 10KB, lightweight
- **shiki** - Already installed, WASM-based but high quality
- **Lowlight with fewer languages** - Already using, just trim unused langs

### For Math:
- **KaTeX** - Current choice, fast but heavy CSS
- **MathJax** - Slower but more complete
- **Custom regex + HTML** - If you only need simple formulas

## Performance Targets

| Metric | Current | After Phase 1 | After All Phases |
|--------|---------|---------------|------------------|
| Initial render (1000 events) | 2-5s | <500ms | <300ms |
| Scroll FPS | 15-30 | 60 | 60 |
| Detail panel render | 500-1000ms | 500-1000ms | 100-200ms |
| Bundle size | ~2MB | ~2MB | ~1.5MB |
| DOM nodes (1000 events) | ~5000 | ~50 | ~50 |

## Risks & Mitigations

**Risk 1: Virtualization breaks scroll-to-selected behavior**
- DebugTab scrolls to selected event (lines 174-194)
- Mitigation: Use virtualizer's `scrollToIndex()` method

**Risk 2: React.memo breaks dynamic content updates**
- EventRow receives callbacks (onClick) that change
- Mitigation: Use stable callbacks with useCallback in parent

**Risk 3: Lazy KaTeX breaks math rendering**
- Math detection regex might miss edge cases
- Mitigation: Conservative detection (any `$` triggers KaTeX load)

**Risk 4: streamdown doesn't support all features**
- May not support math or all Markdown extensions
- Mitigation: Test thoroughly before switching

## Recommendation

**Start with Phase 1 (virtualization)** - this alone will solve 80% of the performance problem. The list rendering is the main bottleneck, not the Markdown.

Then optimize Markdown rendering in Phase 2 if needed.

**Estimated total effort**: 4-6 hours
**Expected result**: 10-50x performance improvement

---

## Next Steps

1. Review this plan
2. Approve Phase 1 implementation (virtualization)
3. Implement and test
4. Measure performance improvement
5. Decide if Phase 2 (Markdown optimization) is needed
