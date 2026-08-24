type Flush = () => void;

export class FrameNotifier {
  private frame?: number;

  constructor(private readonly flush: Flush) {}

  schedule(): void {
    if (this.frame !== undefined) return;
    this.frame = scheduleFrame(() => {
      this.frame = undefined;
      this.flush();
    });
  }

  flushNow(): void {
    if (this.frame !== undefined) {
      cancelFrame(this.frame);
      this.frame = undefined;
    }
    this.flush();
  }

  cancel(): void {
    if (this.frame === undefined) return;
    cancelFrame(this.frame);
    this.frame = undefined;
  }
}

function scheduleFrame(callback: FrameRequestCallback): number {
  if (typeof requestAnimationFrame === "function") {
    return requestAnimationFrame(callback);
  }
  return setTimeout(() => callback(performance.now()), 16) as unknown as number;
}

function cancelFrame(handle: number): void {
  if (typeof cancelAnimationFrame === "function") {
    cancelAnimationFrame(handle);
    return;
  }
  clearTimeout(handle);
}
