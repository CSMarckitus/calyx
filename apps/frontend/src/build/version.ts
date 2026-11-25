export type Version = { version: string; build: string };

export function makeVersion(packageVersion: string, now: Date): Version {
  return { version: packageVersion, build: now.toISOString() };
}

export function versionJson(v: Version): string {
  return `${JSON.stringify({ version: v.version, build: v.build })}\n`;
}
