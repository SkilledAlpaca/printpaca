/** Counts the times the user asked to save or jumped to a blocking field while the quote was incomplete. The
 * Quote tab reads it to reveal required-field errors that stay quiet until a field is left (a freshly
 * estimated quote shouldn't open with red fields). Boxed because an exported `let` can't be reassigned from an
 * importing module. */
export const saveAttempt = $state({ count: 0 });

export function noteSaveAttempt(): void {
  saveAttempt.count += 1;
}
