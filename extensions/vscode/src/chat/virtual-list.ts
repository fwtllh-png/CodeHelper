export interface VirtualListItem {
  readonly height: number;
}

export interface VirtualWindow {
  readonly start: number;
  readonly end: number;
  readonly paddingBefore: number;
  readonly paddingAfter: number;
  readonly totalHeight: number;
}

export function computeVirtualWindow(
  items: readonly VirtualListItem[],
  scrollTop: number,
  viewportHeight: number,
  overscan = 200,
): VirtualWindow {
  if (!Number.isFinite(scrollTop) || scrollTop < 0 ||
    !Number.isFinite(viewportHeight) || viewportHeight < 0 ||
    !Number.isFinite(overscan) || overscan < 0) {
    throw new Error("virtual list viewport is invalid");
  }
  const totalHeight = items.reduce((total, item) => {
    validateHeight(item.height);
    return total + item.height;
  }, 0);
  const effectiveScrollTop = Math.min(
    scrollTop,
    Math.max(0, totalHeight - viewportHeight),
  );
  const lower = Math.max(0, effectiveScrollTop - overscan);
  const upper = Math.min(
    totalHeight,
    effectiveScrollTop + Math.max(1, viewportHeight) + overscan,
  );
  let offset = 0;
  let start = 0;
  while (start < items.length &&
    offset + (items[start]?.height ?? 0) <= lower) {
    offset += items[start]?.height ?? 0;
    start++;
  }
  let end = start;
  let visibleHeight = offset;
  while (end < items.length && visibleHeight < upper) {
    visibleHeight += items[end]?.height ?? 0;
    end++;
  }
  return {
    start,
    end,
    paddingBefore: offset,
    paddingAfter: Math.max(0, totalHeight - visibleHeight),
    totalHeight,
  };
}

export function virtualItemOffset(
  items: readonly VirtualListItem[],
  index: number,
): number {
  if (!Number.isSafeInteger(index) || index < 0 || index >= items.length) {
    throw new Error("virtual list index is invalid");
  }
  validateHeight(items[index]?.height ?? 0);
  let offset = 0;
  for (let current = 0; current < index; current++) {
    const height = items[current]?.height ?? 0;
    validateHeight(height);
    offset += height;
  }
  return offset;
}

function validateHeight(value: number): void {
  if (!Number.isFinite(value) || value <= 0 || value > 10_000) {
    throw new Error("virtual list item height is invalid");
  }
}
