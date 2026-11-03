import type { EnergySchedule } from "./engine/schedule";

/** The structure of Southern California Edison's TOU-D-4-9PM residential plan: a summer (June to
 * September) and winter (October to May) season, each with weekday and weekend hours. Summer weekdays have
 * an on-peak 4 to 9 pm window and summer weekends a mid-peak window at the same hours; winter days have a
 * mid-peak 4 to 9 pm window and a super off-peak 8 am to 4 pm window.
 *
 * The rates are EXAMPLES, not SCE's published rates, which change; they exist to show the shape.
 * testdata/schedule-vectors.json holds the same schedule under the name "sce" (a test keeps them equal).
 * Source for the structure: https://www.sce.com/save-money/rates-financing/residential-rate-plans/time-of-use-plans */
export function sceExampleSchedule(): EnergySchedule {
  const winterDay = () => ({
    baseName: "Off-peak",
    baseRate: 0.34,
    windows: [
      { name: "Super off-peak", start: "08:00", end: "16:00", rate: 0.28 },
      { name: "Mid-peak", start: "16:00", end: "21:00", rate: 0.44 },
    ],
  });
  return {
    defaultStartTime: "22:00",
    holidays: [],
    seasons: [
      {
        name: "Summer",
        months: [6, 7, 8, 9],
        weekday: { baseName: "Off-peak", baseRate: 0.33, windows: [{ name: "On-peak", start: "16:00", end: "21:00", rate: 0.55 }] },
        weekend: { baseName: "Off-peak", baseRate: 0.33, windows: [{ name: "Mid-peak", start: "16:00", end: "21:00", rate: 0.44 }] },
      },
      { name: "Winter", months: [1, 2, 3, 4, 5, 10, 11, 12], weekday: winterDay(), weekend: winterDay() },
    ],
  };
}

/** A blank schedule that passes validation: one all-year season at one flat rate. */
export function blankSchedule(rate = 0.15): EnergySchedule {
  const day = () => ({ baseName: "Standard", baseRate: rate, windows: [] });
  return {
    defaultStartTime: "22:00",
    holidays: [],
    seasons: [{ name: "All year", months: [1, 2, 3, 4, 5, 6, 7, 8, 9, 10, 11, 12], weekday: day(), weekend: day() }],
  };
}
