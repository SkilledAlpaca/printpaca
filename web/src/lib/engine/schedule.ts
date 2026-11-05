// Time-of-use schedule pricing. server/energy_schedule.go is the reference; testdata/schedule-vectors.json
// pins both. A print runs continuously from its start; the span is split at every window edge, midnight and
// season change, and each piece costs hours x rate. All instants are float milliseconds since the epoch and
// the operations run in the same order as the Go code, so both sides agree to the last bit.

export interface TouWindow {
  name: string;
  /** Wall-clock HH:MM. */
  start: string;
  /** Wall-clock HH:MM; "24:00" is allowed. */
  end: string;
  rate: number;
}

export interface TouDay {
  baseName: string;
  baseRate: number;
  windows: TouWindow[];
}

export interface TouSeason {
  name: string;
  months: number[];
  weekday: TouDay;
  weekend: TouDay;
}

export interface EnergySchedule {
  defaultStartTime: string;
  holidays: string[];
  seasons: TouSeason[];
  /** Every season has the same base period names and windows (name, start, end) per day type; only the
   * rates differ. An editing constraint that pricing ignores. Absent when false. */
  sameWindowsAllYear?: boolean;
}

export interface EnergyPeriod {
  name: string;
  hours: number;
  cost: number;
}

export interface ScheduleCost {
  periods: EnergyPeriod[];
  total: number;
}

export const MAX_SCHEDULE_SEASONS = 6;
export const MAX_SCHEDULE_WINDOWS = 8;
export const MAX_SCHEDULE_HOLIDAYS = 100;
export const MAX_SCHEDULE_NAME_CHARS = 40;
const MAX_MONEY_AMOUNT = 1e9;
/** Spans longer than this simulate the first 366 days and scale that average for the rest. */
const SIMULATE_HOURS = 366 * 24;

const HHMM = /^([01][0-9]|2[0-3]):([0-5][0-9])$/;
const DATE = /^\d{4}-\d{2}-\d{2}$/;
const WALL = /^(\d{4})-(\d{2})-(\d{2})T([01][0-9]|2[0-3]):([0-5][0-9])$/;

/** HH:MM to minutes after midnight, or null. allow24 accepts "24:00". */
export function parseClock(s: string, allow24 = false): number | null {
  if (allow24 && s === "24:00") return 1440;
  const m = HHMM.exec(s);
  return m ? Number(m[1]) * 60 + Number(m[2]) : null;
}

export function isValidDate(s: string): boolean {
  if (!DATE.test(s)) return false;
  const [y, m, d] = s.split("-").map(Number) as [number, number, number];
  const t = new Date(Date.UTC(y, m - 1, d));
  return t.getUTCFullYear() === y && t.getUTCMonth() === m - 1 && t.getUTCDate() === d;
}

function validateDay(path: string, d: TouDay): string[] {
  const p: string[] = [];
  if (d.baseName.trim() === "") p.push(`${path}.baseName: required`);
  if ([...d.baseName].length > MAX_SCHEDULE_NAME_CHARS) p.push(`${path}.baseName: at most ${MAX_SCHEDULE_NAME_CHARS} characters`);
  if (!(d.baseRate >= 0) || d.baseRate > MAX_MONEY_AMOUNT) p.push(`${path}.baseRate: must be >= 0`);
  if (d.windows.length > MAX_SCHEDULE_WINDOWS) {
    p.push(`${path}.windows: ${d.windows.length} windows exceeds max ${MAX_SCHEDULE_WINDOWS}`);
    return p;
  }
  const spans: [number, number][] = [];
  d.windows.forEach((w, i) => {
    const wp = `${path}.windows[${i}]`;
    if (w.name.trim() === "") p.push(`${wp}.name: required`);
    if ([...w.name].length > MAX_SCHEDULE_NAME_CHARS) p.push(`${wp}.name: at most ${MAX_SCHEDULE_NAME_CHARS} characters`);
    if (!(w.rate >= 0) || w.rate > MAX_MONEY_AMOUNT) p.push(`${wp}.rate: must be >= 0`);
    const s = parseClock(w.start);
    const e = parseClock(w.end, true);
    if (s === null) p.push(`${wp}.start: must be HH:MM`);
    if (e === null) p.push(`${wp}.end: must be HH:MM (24:00 allowed)`);
    if (s !== null && e !== null) {
      if (s >= e) p.push(`${wp}: start must be before end`);
      else spans.push([s, e]);
    }
  });
  for (let i = 0; i < spans.length; i++) {
    for (let j = i + 1; j < spans.length; j++) {
      if (spans[i]![0] < spans[j]![1] && spans[j]![0] < spans[i]![1]) {
        p.push(`${path}.windows: windows must not overlap`);
        return p;
      }
    }
  }
  return p;
}

/** Whether two day types have the same base name and the same windows (name, start, end). Rates are ignored. */
export function sameTouWindows(a: TouDay, b: TouDay): boolean {
  return (
    a.baseName === b.baseName &&
    a.windows.length === b.windows.length &&
    a.windows.every((w, i) => w.name === b.windows[i]!.name && w.start === b.windows[i]!.start && w.end === b.windows[i]!.end)
  );
}

/** Problems with a schedule, in the server's words (energy.schedule.* paths). An empty list is valid.
 * requireSeasons is true in schedule mode. */
export function validateEnergySchedule(s: EnergySchedule, requireSeasons: boolean): string[] {
  const p: string[] = [];
  if (parseClock(s.defaultStartTime) === null) p.push("energy.schedule.defaultStartTime: must be HH:MM");
  if (s.holidays.length > MAX_SCHEDULE_HOLIDAYS) {
    p.push(`energy.schedule.holidays: ${s.holidays.length} dates exceeds max ${MAX_SCHEDULE_HOLIDAYS}`);
  }
  s.holidays.forEach((h, i) => {
    if (!isValidDate(h)) p.push(`energy.schedule.holidays[${i}]: "${h}" must be a date written YYYY-MM-DD`);
  });
  if (s.seasons.length === 0) {
    if (requireSeasons) p.push("energy.schedule.seasons: add at least one season that covers months 1-12");
    return p;
  }
  if (s.seasons.length > MAX_SCHEDULE_SEASONS) {
    p.push(`energy.schedule.seasons: ${s.seasons.length} seasons exceeds max ${MAX_SCHEDULE_SEASONS}`);
    return p;
  }
  const owner = new Map<number, string>();
  s.seasons.forEach((se, i) => {
    const sp = `energy.schedule.seasons[${i}]`;
    if (se.name.trim() === "") p.push(`${sp}.name: required`);
    if ([...se.name].length > MAX_SCHEDULE_NAME_CHARS) p.push(`${sp}.name: at most ${MAX_SCHEDULE_NAME_CHARS} characters`);
    for (const m of se.months) {
      if (!Number.isInteger(m) || m < 1 || m > 12) {
        p.push(`${sp}.months: ${m} is not a month 1-12`);
        continue;
      }
      const prev = owner.get(m);
      if (prev !== undefined) {
        p.push(`energy.schedule.seasons: month ${m} is in both ${prev} and ${sp}; a month belongs to one season`);
        continue;
      }
      owner.set(m, sp);
    }
    p.push(...validateDay(`${sp}.weekday`, se.weekday), ...validateDay(`${sp}.weekend`, se.weekend));
  });
  if (s.sameWindowsAllYear) {
    s.seasons.forEach((se, i) => {
      if (i === 0) return;
      for (const dt of ["weekday", "weekend"] as const) {
        if (!sameTouWindows(s.seasons[0]![dt], se[dt])) {
          p.push(`energy.schedule.seasons[${i}].${dt}.windows: sameWindowsAllYear is on, so the windows must match seasons[0] (rates may differ)`);
        }
      }
    });
  }
  const missing: number[] = [];
  for (let m = 1; m <= 12; m++) if (!owner.has(m)) missing.push(m);
  if (missing.length > 0) {
    p.push(`energy.schedule.seasons: month(s) ${missing.join(", ")} are in no season; every month 1-12 needs one`);
  }
  return p;
}

// --- time zone arithmetic ---------------------------------------------------------------------------------

const formatters = new Map<string, Intl.DateTimeFormat>();

function formatter(tz: string): Intl.DateTimeFormat {
  let f = formatters.get(tz);
  if (!f) {
    f = new Intl.DateTimeFormat("en-US", {
      timeZone: tz,
      hourCycle: "h23",
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      second: "2-digit",
    });
    formatters.set(tz, f);
  }
  return f;
}

export function isValidTimezone(tz: string): boolean {
  try {
    formatter(tz || "UTC");
    return tz !== "Local";
  } catch {
    return false;
  }
}

interface Wall {
  y: number;
  mo: number;
  d: number;
  h: number;
  mi: number;
  s: number;
}

function wallParts(tz: string, ms: number): Wall {
  const parts = formatter(tz).formatToParts(new Date(Math.floor(ms)));
  const get = (t: string) => Number(parts.find((p) => p.type === t)?.value);
  return { y: get("year"), mo: get("month"), d: get("day"), h: get("hour"), mi: get("minute"), s: get("second") };
}

function offsetMs(tz: string, ms: number): number {
  const w = wallParts(tz, ms);
  return Date.UTC(w.y, w.mo - 1, w.d, w.h, w.mi, w.s) - Math.floor(ms / 1000) * 1000;
}

/** A wall-clock date plus minutes after midnight (1440 and beyond roll into later days) in tz as an instant.
 * Around a DST change a repeated wall time resolves to the earlier instant and a skipped one (inside the
 * spring-forward gap) to the first instant after the gap, matching the server. */
export function wallToInstant(tz: string, y: number, mo: number, d: number, minutes: number): number {
  const wall = Date.UTC(y, mo - 1, d, 0, minutes);
  const oA = offsetMs(tz, wall - 86400000);
  const oB = offsetMs(tz, wall + 86400000);
  const tA = wall - oA;
  const tB = wall - oB;
  const okA = offsetMs(tz, tA) === oA;
  const okB = offsetMs(tz, tB) === oB;
  if (okA && okB) return Math.min(tA, tB);
  if (okA) return tA;
  if (okB) return tB;
  // Inside the spring-forward gap: tB (read with the later offset) is before the change and tA is after it.
  // Clamp to the first instant whose offset is no longer oA, found by bisection to the millisecond.
  let lo = tB;
  let hi = tA;
  while (hi - lo > 1) {
    const mid = Math.floor((lo + hi) / 2);
    if (offsetMs(tz, mid) === oA) lo = mid;
    else hi = mid;
  }
  return hi;
}

/** "YYYY-MM-DDTHH:MM" in tz to an instant, or null when it is not a real date and time. */
export function parseWallStart(s: string, tz: string): number | null {
  const m = WALL.exec(s);
  if (!m) return null;
  const [y, mo, d, h, mi] = [Number(m[1]), Number(m[2]), Number(m[3]), Number(m[4]), Number(m[5])] as const;
  if (!isValidDate(`${m[1]}-${m[2]}-${m[3]}`)) return null;
  return wallToInstant(tz, y, mo, d, h * 60 + mi);
}

const pad = (n: number, w = 2) => String(n).padStart(w, "0");

/** The next occurrence of the HH:MM clock strictly after `now`, as wall-clock "YYYY-MM-DDTHH:MM" in tz. */
export function nextStartWall(clock: string, tz: string, now: Date = new Date()): string {
  const c = parseClock(clock) === null ? "22:00" : clock;
  const w = wallParts(tz, now.getTime());
  const today = `${pad(w.y, 4)}-${pad(w.mo)}-${pad(w.d)}`;
  if (`${today}T${c}` > `${today}T${pad(w.h)}:${pad(w.mi)}`) return `${today}T${c}`;
  const next = new Date(Date.UTC(w.y, w.mo - 1, w.d + 1));
  return `${pad(next.getUTCFullYear(), 4)}-${pad(next.getUTCMonth() + 1)}-${pad(next.getUTCDate())}T${c}`;
}

/** Wall-clock "YYYY-MM-DDTHH:MM" in tz for an instant (the inverse a datetime-local input needs). */
export function wallFromInstant(tz: string, ms: number): string {
  const w = wallParts(tz, ms);
  return `${pad(w.y, 4)}-${pad(w.mo)}-${pad(w.d)}T${pad(w.h)}:${pad(w.mi)}`;
}

// --- cost -------------------------------------------------------------------------------------------------

interface Win {
  s: number;
  e: number;
  name: string;
  rate: number;
}

interface Day {
  base: TouDay;
  edges: number[];
  wins: Win[];
}

function buildDay(d: TouDay): Day {
  const wins: Win[] = [];
  const set = new Set<number>([1440]);
  for (const w of d.windows) {
    const s = parseClock(w.start) ?? 0;
    const e = parseClock(w.end, true) ?? 0;
    wins.push({ s, e, name: w.name, rate: w.rate });
    set.add(s);
    set.add(e);
  }
  return { base: d, edges: [...set].sort((a, b) => a - b), wins };
}

function dayAt(day: Day, minute: number): { rate: number; name: string; next: number } {
  let rate = day.base.baseRate;
  let name = day.base.baseName;
  for (const w of day.wins) {
    if (w.s <= minute && minute < w.e) {
      rate = w.rate;
      name = w.name;
      break;
    }
  }
  let next = 1440;
  for (const b of day.edges) {
    if (b > minute) {
      next = b;
      break;
    }
  }
  return { rate, name, next };
}

/** Prices a continuous print of spanHours starting at the wall-clock start in tz on a printer drawing kW.
 * Returns null when the schedule, zone or start is unusable. Periods come in order of first use. */
export function scheduleEnergy(
  s: EnergySchedule,
  tz: string,
  startWall: string,
  spanHours: number,
  kW: number,
): ScheduleCost | null {
  if (validateEnergySchedule(s, true).length > 0 || !isValidTimezone(tz)) return null;
  const startMs = parseWallStart(startWall, tz);
  if (startMs === null || Number.isNaN(spanHours) || Number.isNaN(kW)) return null;
  if (spanHours <= 0) return { periods: [], total: 0 };

  const simHours = Math.min(spanHours, SIMULATE_HOURS);
  const end = startMs + simHours * 3600000;
  const seasonOf = new Map<number, number>();
  s.seasons.forEach((se, i) => se.months.forEach((m) => seasonOf.set(m, i)));
  const holiday = new Set(s.holidays);
  const days = new Map<number, Day>();

  const order: { name: string; hours: number; rate: number }[] = [];
  const byName = new Map<string, { name: string; hours: number; rate: number }>();

  let cur = startMs;
  while (cur < end) {
    const w = wallParts(tz, cur);
    const minute = w.h * 60 + w.mi;
    const dateKey = `${pad(w.y, 4)}-${pad(w.mo)}-${pad(w.d)}`;
    const dow = new Date(Date.UTC(w.y, w.mo - 1, w.d, 12)).getUTCDay();
    const weekend = dow === 0 || dow === 6 || holiday.has(dateKey);
    const si = seasonOf.get(w.mo) ?? 0;
    const key = si * 2 + (weekend ? 1 : 0);
    let day = days.get(key);
    if (!day) {
      day = buildDay(weekend ? s.seasons[si]!.weekend : s.seasons[si]!.weekday);
      days.set(key, day);
    }
    const { rate, name, next } = dayAt(day, minute);
    let segEnd = wallToInstant(tz, w.y, w.mo, w.d, next);
    if (segEnd <= cur) segEnd = cur + 60000;
    if (segEnd > end) segEnd = end;
    const hours = (segEnd - cur) / 3600000;
    let a = byName.get(name);
    if (!a) {
      a = { name, hours: 0, rate: 0 };
      byName.set(name, a);
      order.push(a);
    }
    a.hours += hours;
    a.rate += hours * rate;
    cur = segEnd;
  }

  const scale = spanHours / simHours;
  const periods: EnergyPeriod[] = [];
  let rateHours = 0;
  for (const a of order) {
    const h = a.hours * scale;
    const rh = a.rate * scale;
    periods.push({ name: a.name, hours: h, cost: kW * rh });
    rateHours += rh;
  }
  return { periods, total: kW * rateHours };
}
