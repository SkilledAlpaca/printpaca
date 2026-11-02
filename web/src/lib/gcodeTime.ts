/** Splits seconds into hours and minutes, rounding at the minute level first so 3595s becomes 1h 0m, not 0h
 * 60m (60 minutes fails the validator's max of 59 and blanks the quote). */
export function splitSeconds(timeSeconds: number): { hours: number; minutes: number } {
  const mins = Math.round(timeSeconds / 60);
  return { hours: Math.floor(mins / 60), minutes: mins % 60 };
}
