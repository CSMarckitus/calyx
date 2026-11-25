// The phone number of a pairing by code. The page keeps only the digits.

const digits = (text: string) => text.replace(/\D/g, "");

// The complete number: the country code and the number, digits only, with no "+". A text that
// starts with + or 00 is already complete. Otherwise the dialling code of the country comes first.
export function fullNumber(text: string, dial: string): string {
  const trimmed = text.trim();
  if (trimmed.startsWith("+")) return digits(trimmed);
  if (trimmed.startsWith("00")) return digits(trimmed.slice(2));
  const rest = digits(trimmed);
  return rest === "" ? "" : dial + rest;
}

// The backend accepts 7 to 15 digits.
export const isPhoneValid = (number: string) => /^\d{7,15}$/.test(number);

export const showNumber = (number: string) => `+${number}`;
