export interface TranscriptWindow {
  readonly start: number;
  readonly end: number;
  readonly paddingBefore: number;
  readonly paddingAfter: number;
}

export const transcriptTurnEstimate = 180;

export function computeTranscriptWindow(
  total: number,
  scrollTop: number,
  viewportHeight: number,
  estimate = transcriptTurnEstimate,
  overscan = 8,
): TranscriptWindow {
  if (!Number.isSafeInteger(total) || total < 0 ||
    !Number.isFinite(scrollTop) || scrollTop < 0 ||
    !Number.isFinite(viewportHeight) || viewportHeight < 0 ||
    !Number.isFinite(estimate) || estimate <= 0 ||
    !Number.isSafeInteger(overscan) || overscan < 0) {
    throw new Error("Transcript viewport is invalid");
  }
  const count = Math.ceil(viewportHeight / estimate) + overscan * 2;
  const candidate = Math.max(
    0,
    Math.floor(scrollTop / estimate) - overscan,
  );
  const start = Math.min(Math.max(0, total - count), candidate);
  const end = Math.min(total, start + count);
  return {
    start,
    end,
    paddingBefore: start * estimate,
    paddingAfter: (total - end) * estimate,
  };
}
