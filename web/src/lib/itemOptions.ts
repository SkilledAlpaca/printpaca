/** Pure logic for item option groups, shared by the public order panel, the admin Items tab, and the invoice
 * editor. Mirrors server/itemoptions.go: the server stays the price authority and re-validates everything. */

/** An option as the public order panel sees it. */
export interface ChoiceOption {
  id: string;
  name: string;
  deltaCents: number;
  /** "/item-images/{file}" or "" / absent when the option has no photo. */
  imageUrl?: string;
}

export interface ChoiceGroup {
  id: string;
  label: string;
  required: boolean;
  options: ChoiceOption[];
}

/** groupId -> optionId; a group with no entry (or "") is unanswered. */
export type Selected = Record<string, string>;

export const OPTION_SUMMARY_SEP = " · ";

function chosenOption(group: ChoiceGroup, selected: Selected): ChoiceOption | undefined {
  const id = selected[group.id];
  return id ? group.options.find((o) => o.id === id) : undefined;
}

/** Sum of the selected options' price changes, in cents. */
export function selectedDeltaCents(groups: ChoiceGroup[], selected: Selected): number {
  let sum = 0;
  for (const g of groups) sum += chosenOption(g, selected)?.deltaCents ?? 0;
  return sum;
}

/** Required groups with no valid choice, in display order. */
export function missingRequired(groups: ChoiceGroup[], selected: Selected): ChoiceGroup[] {
  return groups.filter((g) => g.required && !chosenOption(g, selected));
}

/** The order request's `selections`: answered groups only, in display order. */
export function selectionsPayload(groups: ChoiceGroup[], selected: Selected): { groupId: string; optionId: string }[] {
  const out: { groupId: string; optionId: string }[] = [];
  for (const g of groups) {
    const o = chosenOption(g, selected);
    if (o) out.push({ groupId: g.id, optionId: o.id });
  }
  return out;
}

/** "Color: Red · Size: Large": answered groups in display order, the form the server stores as option_name. */
export function optionSummary(groups: ChoiceGroup[], selected: Selected): string {
  const parts: string[] = [];
  for (const g of groups) {
    const o = chosenOption(g, selected);
    if (o) parts.push(`${g.label}: ${o.name}`);
  }
  return parts.join(OPTION_SUMMARY_SEP);
}

/** Records that `groupId` was just changed: moves it to the front of the recency list. */
export function touchRecent(recent: string[], groupId: string): string[] {
  return [groupId, ...recent.filter((g) => g !== groupId)];
}

/** The photo to show: the most recently changed selection that has one, else null (callers fall back to the
 * item photo). `alt` names the option so the swap is announced to screen reader users browsing the image. */
export function pickDisplayImage(
  groups: ChoiceGroup[],
  selected: Selected,
  recent: string[],
): { url: string; alt: string } | null {
  for (const gid of recent) {
    const g = groups.find((x) => x.id === gid);
    const o = g ? chosenOption(g, selected) : undefined;
    if (g && o?.imageUrl) return { url: o.imageUrl, alt: `${g.label}: ${o.name}` };
  }
  return null;
}

/** The starting choice for a staff form: a required group's first option, an optional group unanswered. */
export function defaultSelection(groups: ChoiceGroup[]): Selected {
  const out: Selected = {};
  for (const g of groups) if (g.required && g.options[0]) out[g.id] = g.options[0].id;
  return out;
}

/** Returns a copy of `list` with the element at `index` moved by `delta` (-1 up, +1 down); the same array
 * when the move would leave the list. */
export function moveItem<T>(list: T[], index: number, delta: number): T[] {
  const to = index + delta;
  if (index < 0 || index >= list.length || to < 0 || to >= list.length) return list;
  const out = list.slice();
  out.splice(to, 0, ...out.splice(index, 1));
  return out;
}

/** Parses a stored option summary back into per-group selections (groupId -> optionId), or null when it isn't
 * a valid summary for `groups`. Backtracking, like the server, so labels or names containing ": " or the
 * separator stay unambiguous. "" parses to no selections. */
export function parseOptionSummary(groups: ChoiceGroup[], summary: string): Selected | null {
  if (summary === "") return {};
  const used = new Set<string>();
  const out: Selected = {};
  function walk(rest: string): boolean {
    for (const g of groups) {
      if (used.has(g.id)) continue;
      for (const o of g.options) {
        const part = `${g.label}: ${o.name}`;
        if (rest === part) {
          out[g.id] = o.id;
          return true;
        }
        if (rest.startsWith(part + OPTION_SUMMARY_SEP)) {
          used.add(g.id);
          out[g.id] = o.id;
          if (walk(rest.slice(part.length + OPTION_SUMMARY_SEP.length))) return true;
          delete out[g.id];
          used.delete(g.id);
        }
      }
    }
    return false;
  }
  return walk(summary) ? out : null;
}
