import type { Sandbox } from "@/lib/types";

/**
 * A sandbox given whole GPUs (capability gpu). Its VMM cannot save a
 * passed-through device's state, so sandboxd refuses to suspend, snapshot or
 * resize it, and Studio does not offer to.
 */
export function hasGPU(sb: Pick<Sandbox, "gpus">): boolean {
  return (sb.gpus?.length ?? 0) > 0;
}

/** Why a GPU sandbox has no suspend, snapshot or resize. */
export const GPU_FIXED = "A sandbox with a GPU cannot be suspended, snapshotted or resized: its VMM cannot save a passed-through device's state.";
