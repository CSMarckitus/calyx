// The steps in WhatsApp that link a device. WhatsApp changes its menus, so they are in this one
// file, and a person compares them with the phone before a release.
// A name of a menu or a button is between asterisks. The screen shows it in bold.

export type Phone = "android" | "iphone";
export type Method = "code" | "qr";

export const phoneNames: Record<Phone, string> = { android: "Android", iphone: "iPhone" };

const open: Record<Method, string> = {
  code: "Open WhatsApp on this phone.",
  qr: "Open WhatsApp on your phone.",
};

const menu: Record<Phone, string> = {
  android: "Tap *More options* (the three dots), then *Linked devices*.",
  iphone: "Open *Settings*, then *Linked devices*.",
};

const end: Record<Method, string[]> = {
  code: ["Tap *Link a device*, then *Link with phone number instead*.", "Type the code."],
  qr: ["Tap *Link a device*, and scan this QR code."],
};

export function phoneSteps(phone: Phone, method: Method): string[] {
  return [open[method], menu[phone], ...end[method]];
}

// An iPhone or an iPad says so in its user agent. Any other phone is Android, the most common one.
export function guessPhone(userAgent = globalThis.navigator?.userAgent ?? ""): Phone {
  return /iPhone|iPad|iPod/.test(userAgent) ? "iphone" : "android";
}
