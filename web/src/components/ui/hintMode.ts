import { getContext, setContext } from "svelte";
import type { HintMode } from "./hintTip";

export const HINT_MODE_KEY = Symbol("printpaca-hint-mode");

/** The customer shop's entry calls this once so every Field below it keeps its hint visible. */
export function setHintMode(mode: HintMode): void {
  setContext(HINT_MODE_KEY, mode);
}

/** The mode set by an ancestor; the admin app sets none and gets tooltips. */
export function getHintMode(): HintMode {
  return getContext<HintMode | undefined>(HINT_MODE_KEY) ?? "tooltip";
}
