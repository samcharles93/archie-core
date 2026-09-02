import { h } from "preact";

/**
 * Status pill: a small labeled badge whose color follows a semantic kind
 * (ok/warn/danger/info/idle -- see css/pill.css). Extracted from 11
 * independent, byte-identical local copies (tasks, logs, mappings,
 * captures, settings, update-status, skills, dashboard, memory, channels,
 * curators) per organisation.md's "extract on the second distinct
 * consumer" rule -- archie-core-mlro.
 */
export function Pill({ text, kind = "idle" }) {
  return <span className={`pill pill-${kind}`}>{text}</span>;
}
