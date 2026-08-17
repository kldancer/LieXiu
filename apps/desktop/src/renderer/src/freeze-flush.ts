import type { FreezeBreadcrumb } from "../../shared/freeze-breadcrumb";

export interface FlushFreezeBreadcrumbDeps {
  getLastFreeze: () => FreezeBreadcrumb | null;
  ackFreeze: (ts: number) => void;
}

/**
 * Consume a pending local freeze/crash breadcrumb and retire that exact slot.
 * There is intentionally no capture callback or delayed network hand-off:
 * local diagnostics remain local after analytics removal.
 */
export function flushFreezeBreadcrumb({
  getLastFreeze,
  ackFreeze,
}: FlushFreezeBreadcrumbDeps): () => void {
  const last = getLastFreeze();
  if (!last) return () => undefined;

  ackFreeze(last.ts);
  return () => undefined;
}
