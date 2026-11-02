// Per-card help visibility for the Quote tab's `?` toggles, persisted to localStorage; never throws, falls
// back to in-memory state (Safari private mode, quota).

const STORAGE_KEY = "printpaca-help";

function readLocal(): Record<string, boolean> {
  try {
    const raw = localStorage.getItem(STORAGE_KEY);
    if (!raw) return {};
    const parsed = JSON.parse(raw) as unknown;
    if (parsed && typeof parsed === "object" && !Array.isArray(parsed)) {
      return parsed as Record<string, boolean>;
    }
    return {};
  } catch {
    return {};
  }
}

function writeLocal(open: Record<string, boolean>): void {
  try {
    localStorage.setItem(STORAGE_KEY, JSON.stringify(open));
  } catch {
  }
}

export const helpState = $state({ open: readLocal() as Record<string, boolean> });

export function setHelp(id: string, v: boolean): void {
  helpState.open[id] = v;
  writeLocal(helpState.open);
}

export function setAllHelp(ids: string[], v: boolean): void {
  for (const id of ids) helpState.open[id] = v;
  writeLocal(helpState.open);
}
