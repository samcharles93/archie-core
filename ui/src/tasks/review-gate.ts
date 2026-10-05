export interface ReviewGate {
  head_sha?: string;
  outcome?: string;
  findings: {
    key: string;
    finding: {
      Title: string;
      Body: string;
      File: string;
      LineStart: number;
      LineEnd: number;
      Severity: string;
      Score: number;
      Confidence: number;
      Evidence?: string;
      Suggestion?: string;
      Blocking?: boolean;
    };
  }[];
}

export function reviewGateOffer(stored?: string): ReviewGate | null {
  if (!stored) return null;
  try {
    const gate = JSON.parse(stored) as ReviewGate;
    return !gate.outcome && Array.isArray(gate.findings) && gate.findings.length ? gate : null;
  } catch {
    return null;
  }
}
