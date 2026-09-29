import type { ChatMedia } from "./state";

/**
 * How a transcript renders an attachment whose bytes it does not have.
 *
 * The bytes are turn-scoped and never reach a browser, so the label stands in
 * for the file. It reuses the placeholder vocabulary the channels already
 * write into a message's text (`[photo]`, `[document: report.pdf]` — see the
 * notes in `internal/channels/telegram/inboundmedia.go`), so the same
 * attachment reads the same whether its metadata survived persistence or only
 * the note in its text did.
 */
export function mediaLabel(attachment: ChatMedia): string {
  switch (attachment.type) {
    case "image":
      return "[photo]";
    case "video":
      return "[video]";
    case "audio":
      return "[audio]";
    case "document":
      return attachment.file_name
        ? `[document: ${attachment.file_name}]`
        : "[document]";
    default:
      return `[${attachment.type || "attachment"}]`;
  }
}

/**
 * The metadata behind the label, for the chip's tooltip: mime type, file name,
 * size, dimensions and duration, in the order a reader asks about them.
 *
 * It reports only what the record actually carried, so an unknown size reads
 * as absent rather than as zero, and it never names the platform file id — a
 * handle is not information about the file.
 */
export function mediaDetail(attachment: ChatMedia): string {
  const parts: string[] = [];
  if (attachment.mime_type) parts.push(attachment.mime_type);
  if (attachment.file_name) parts.push(attachment.file_name);
  if (attachment.file_size !== undefined) {
    parts.push(`${attachment.file_size} bytes`);
  }
  if (attachment.width && attachment.height) {
    parts.push(`${attachment.width}×${attachment.height}`);
  }
  if (attachment.duration !== undefined) parts.push(`${attachment.duration} s`);
  return parts.join(" · ");
}
