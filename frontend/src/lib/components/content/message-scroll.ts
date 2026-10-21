const LATEST_EDGE_TOLERANCE_PX = 8;

export interface ScrollMetrics {
  scrollTop: number;
  scrollHeight: number;
  clientHeight: number;
}

export type ScrollAlign = "start" | "end";

export function getAlignedOffsetScrollAlign(_align: ScrollAlign): "start" {
  return "start";
}

export function getLatestDisplayIndex(displayCount: number, newestFirst: boolean): number {
  if (displayCount <= 0) return -1;
  return newestFirst ? 0 : displayCount - 1;
}

/** Whether the list is scrolled to its first row. */
export function isAtListStart(metrics: Pick<ScrollMetrics, "scrollTop">): boolean {
  return metrics.scrollTop <= LATEST_EDGE_TOLERANCE_PX;
}

export function isAtLatestEdge(metrics: ScrollMetrics, newestFirst: boolean): boolean {
  if (newestFirst) {
    return isAtListStart(metrics);
  }

  return (
    metrics.scrollHeight - metrics.clientHeight - metrics.scrollTop <= LATEST_EDGE_TOLERANCE_PX
  );
}
