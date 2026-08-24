export const experience = Object.freeze({
  layout: {
    sidebarCollapsed: 56,
    sidebarDefault: 280,
    sidebarMinimum: 264,
    sidebarMaximum: 420,
    centerIdealMinimum: 640,
    chatContent: 748,
    composer: 780,
    disclosureRow: 24,
    trajectoryRow: 30
  },
  motion: {
    panel: 300,
    crossfade: 150,
    disclosure: 100,
    shimmer: 1_800,
    toolSweep: 2_600,
    pending: 1_000
  },
  scrolling: {
    followThreshold: 24
  },
  trajectory: {
    overscanViewports: 2,
    minimumDOMBudget: 72,
    spanMinimumPixels: 2,
    initialViewportHeight: 600,
    dragThresholdFraction: 0.003,
    minimumZoomOperations: 4,
    zoomInFactor: 0.8,
    zoomOutFactor: 1.25
  }
});

export function trajectoryDOMBudget(viewportHeight: number): number {
  const visibleRows = Math.max(
    1,
    Math.ceil(viewportHeight / experience.layout.trajectoryRow)
  );
  return Math.max(
    experience.trajectory.minimumDOMBudget,
    visibleRows * (1 + experience.trajectory.overscanViewports * 2)
  );
}
