import { parseClock, validateEnergySchedule, type EnergySchedule, type TouDay } from "./engine/schedule";

/** A schedule problem in the user's terms. `path` is the server path without the `energy.schedule.` prefix
 * (for example `seasons[1].weekday.windows[0].start`), used to mark the matching input. */
export interface ScheduleProblem {
  path: string;
  text: string;
}

const DAY_LABEL: Record<string, string> = { weekday: "Weekday", weekend: "Weekend and holidays" };
const FIELD_LABEL: Record<string, string> = {
  name: "name",
  baseName: "base period name",
  baseRate: "base rate",
  rate: "rate",
  start: "start",
  end: "end",
  months: "months",
  defaultStartTime: "default print start",
  holidays: "holidays",
};

/** "seasons[1].weekday.windows[0]" to "Season 2 › Weekday › Window 1". */
function describePath(path: string): string {
  const parts: string[] = [];
  const segs = path.split(".");
  for (const seg of segs) {
    const m = /^(\w+?)(?:\[(\d+)\])?$/.exec(seg);
    if (!m) continue;
    const key = m[1]!;
    const idx = m[2] === undefined ? null : Number(m[2]) + 1;
    if (key === "seasons") parts.push(idx === null ? "Seasons" : `Season ${idx}`);
    else if (key === "weekday" || key === "weekend") parts.push(DAY_LABEL[key]!);
    else if (key === "windows") parts.push(idx === null ? "Windows" : `Window ${idx}`);
    else if (key === "holidays") parts.push(idx === null ? "Holidays" : `Holiday ${idx}`);
    else parts.push(FIELD_LABEL[key] ?? key);
  }
  return parts.join(" › ");
}

function plainMessage(msg: string): string {
  return msg
    .replace(/^must be HH:MM \(24:00 allowed\)$/, "enter a time like 16:00 (24:00 is allowed for the end of the day)")
    .replace(/^must be HH:MM$/, "enter a time like 16:00")
    .replace(/^required$/, "can't be empty")
    .replace(/^must be >= 0$/, "must be 0 or more")
    .replace(/^start must be before end$/, "the start must be earlier than the end")
    .replace(/^windows must not overlap$/, "windows can't overlap")
    .replace(/^at most (\d+) characters$/, "use at most $1 characters")
    .replace(/^(\d+) windows exceeds max (\d+)$/, "$1 windows is more than the limit of $2")
    .replace(/^(\d+) seasons exceeds max (\d+)$/, "$1 seasons is more than the limit of $2")
    .replace(/^(\d+) dates exceeds max (\d+)$/, "$1 dates is more than the limit of $2")
    .replace(/^"(.*)" must be a date written YYYY-MM-DD$/, 'write "$1" as a date like 2026-12-25')
    .replace(/^add at least one season that covers months 1-12$/, "add at least one season that covers all 12 months")
    .replace(/^month\(s\) ([\d, ]+) are in no season; every month 1-12 needs one$/, "months $1 are in no season; every month needs one (1 is January)")
    .replace(/^month (\d+) is in both seasons\[(\d+)\] and seasons\[(\d+)\]; a month belongs to one season$/, (_m, mo, a, b) => `month ${mo} is in both Season ${Number(a) + 1} and Season ${Number(b) + 1}; a month belongs to one season`)
    .replace(/^(\d+) is not a month 1-12$/, "$1 is not a month from 1 to 12");
}

/** Rewrites one `energy.schedule.<path>: message` problem (or a bare message) in UI terms. */
export function scheduleProblemUi(raw: string): ScheduleProblem {
  const body = raw.replace(/^energy\.schedule\./, "").replace(/^energy\.schedule$/, "");
  const colon = body.indexOf(": ");
  if (colon < 0) return { path: "", text: body };
  const path = body.slice(0, colon);
  const msg = plainMessage(body.slice(colon + 2));
  const where = describePath(path);
  return { path, text: where ? `${where}: ${msg}` : msg };
}

/** The window times for the paths a problem marks: a problem on `…windows[0]` (start before end) marks both
 * time inputs, one on `…windows` (overlap) marks every window's times. */
export function marksPath(problems: ScheduleProblem[], path: string): boolean {
  return problems.some((p) => {
    if (p.path === path) return true;
    // A window-level problem marks the window's start and end; a windows-level one marks every window in it.
    if (path.endsWith(".start") || path.endsWith(".end")) {
      const win = path.replace(/\.(start|end)$/, "");
      if (p.path === win) return true;
      if (p.path === win.replace(/\[\d+\]$/, "")) return true;
    }
    return false;
  });
}

function splitDay(day: TouDay): number {
  let n = 0;
  const out: TouDay["windows"] = [];
  for (const w of day.windows) {
    const s = parseClock(w.start);
    const e = parseClock(w.end);
    if (s !== null && e !== null && e === 0 && s > 0) {
      // "22:00 to 00:00" is the rest of the day.
      out.push({ ...w, end: "24:00" });
      n++;
    } else if (s !== null && e !== null && s > e) {
      out.push({ ...w, end: "24:00" }, { ...w, start: "00:00" });
      n++;
    } else {
      out.push(w);
    }
  }
  day.windows = out;
  return n;
}

/** Splits every window that runs past midnight (start later than end) into two windows at midnight, which
 * is the only form the server accepts. Returns a copy and the number of windows split. */
export function splitOvernightWindows(schedule: EnergySchedule): { schedule: EnergySchedule; split: number } {
  const copy = structuredClone(schedule);
  let split = 0;
  for (const season of copy.seasons) {
    split += splitDay(season.weekday);
    split += splitDay(season.weekend);
  }
  return { schedule: copy, split };
}

/** The window paths (`seasons[0].weekday.windows[1]`) that run past midnight, for the explanatory note. */
export function overnightWindowPaths(schedule: EnergySchedule): string[] {
  const out: string[] = [];
  schedule.seasons.forEach((season, si) => {
    for (const dt of ["weekday", "weekend"] as const) {
      season[dt].windows.forEach((w, wi) => {
        const s = parseClock(w.start);
        const e = parseClock(w.end);
        if (s !== null && e !== null && ((e === 0 && s > 0) || s > e)) out.push(`seasons[${si}].${dt}.windows[${wi}]`);
      });
    }
  });
  return out;
}

/** Problems as the user should read them: overnight windows are accepted (they are split at save). */
export function scheduleProblems(schedule: EnergySchedule, requireSeasons: boolean): ScheduleProblem[] {
  const { schedule: normalized } = splitOvernightWindows(schedule);
  return validateEnergySchedule(normalized, requireSeasons).map(scheduleProblemUi);
}

export { describePath };
