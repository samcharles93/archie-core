// prettyPrint indents valid JSON so a captured payload can be read as a
// structure rather than a line. A payload that is not JSON is returned as it
// arrived: the receiver stores whatever it is sent, JSON or not
// (docs/prds/webhook-intake-security.md), and the non-JSON case is the one an
// operator most needs to see.
export function prettyPrint(raw: string | undefined): string {
  if (!raw) return "";
  try {
    return JSON.stringify(JSON.parse(raw), null, 2);
  } catch {
    return raw;
  }
}
