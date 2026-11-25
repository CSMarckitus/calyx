// headers.conf is nginx syntax: add_header Name "value" always;
// headers.conf and headers-docs.conf are the sources of the headers, so the preview parses the same files.
export function parseHeaders(conf: string): Record<string, string> {
  const headers: Record<string, string> = {};
  for (const line of conf.split("\n")) {
    const m = /^\s*add_header\s+(\S+)\s+"(.*)"\s*(?:always\s*)?;\s*$/.exec(line);
    if (m) headers[m[1]] = m[2];
  }
  return headers;
}
