import { applyDensity, loadDensity, saveDensity, type Density } from "./density";

// Reactive admin density. Importing this module reads the stored choice and applies it to <html> immediately,
// before the first render, so the admin never flashes the wrong density. Only App.svelte (the admin shell)
// imports it; the public entry never does, which is what keeps the public pages comfortable.
export const density = $state<{ current: Density }>({ current: loadDensity() });
applyDensity(density.current);

export function setDensity(next: Density): void {
  density.current = next;
  applyDensity(next);
  saveDensity(next);
}

export function toggleDensity(): void {
  setDensity(density.current === "compact" ? "comfortable" : "compact");
}
