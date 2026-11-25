// The rule of the backend for a new password (auth.ValidatePassword). The page checks it first,
// so a form never sends a password that the backend refuses.
export const passwordHint = "At least 10 characters.";

const min = 10;
const max = 128;

// The text for the field, or null when the password is good. The backend counts runes, so the
// page counts code points.
export function checkPassword(password: string): string | null {
  const length = [...password].length;
  if (length < min) return passwordHint;
  if (length > max) return `At most ${max} characters.`;
  return null;
}
