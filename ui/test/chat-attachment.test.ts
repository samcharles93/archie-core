import assert from "node:assert/strict";
import { readFile } from "node:fs/promises";
import test from "node:test";

import { mediaDetail, mediaLabel, type ChatMedia } from "../src/chat/attachment.ts";

// The transcript cannot show an attachment's bytes -- they are turn-scoped and
// never reach a browser -- so the label is the entire rendering. It reuses the
// placeholder vocabulary the channels write into a message's text, so a photo
// reads the same whether its metadata survived or only its note did.
test("an attachment is labelled in the channel's own placeholder vocabulary", () => {
  const cases: Array<[ChatMedia, string]> = [
    [{ type: "image", mime_type: "image/jpeg" }, "[photo]"],
    [{ type: "video" }, "[video]"],
    [{ type: "audio" }, "[audio]"],
    [{ type: "document", file_name: "report.pdf" }, "[document: report.pdf]"],
    [{ type: "document" }, "[document]"],
    [{ type: "sticker" }, "[sticker]"],
    [{ type: "" }, "[attachment]"],
  ];
  for (const [attachment, want] of cases) {
    assert.equal(mediaLabel(attachment), want, JSON.stringify(attachment));
  }
});

// The label says what arrived; the detail says what is known about it, which
// is the metadata persistence kept. Nothing here is fetchable, and nothing
// here is invented from a file id.
test("the detail names the metadata that survived persistence", () => {
  assert.equal(
    mediaDetail({
      type: "image",
      mime_type: "image/jpeg",
      file_name: "beach.jpg",
      file_size: 2024,
      width: 1280,
      height: 960,
    }),
    "image/jpeg · beach.jpg · 2024 bytes · 1280×960",
  );
  assert.equal(mediaDetail({ type: "audio", duration: 12 }), "12 s");
  assert.equal(mediaDetail({ type: "document" }), "");
});

// A file id is a platform download handle, not an address: a chip that linked
// it would 404. The chip links out only when the attachment itself carried a
// URL, and the transcript reaches the label and the URL through this module.
test("the transcript renders attachments from metadata, never from a file id", async () => {
  const chip = await readFile(
    new URL("../src/chat/ChatAttachment.vue", import.meta.url),
    "utf8",
  );
  assert.match(chip, /mediaLabel\(attachment\)/, "the chip shows the label");
  assert.match(chip, /mediaDetail\(attachment\)/, "the chip carries the metadata detail");
  assert.match(chip, /:href="attachment\.url"/, "the only link target is a URL the attachment had");
  assert.doesNotMatch(chip, /file_?id/i, "a file id is a handle, not a link target");

  const bubble = await readFile(
    new URL("../src/chat/ChatBubble.vue", import.meta.url),
    "utf8",
  );
  assert.match(bubble, /ChatAttachment/, "the bubble renders the attachment chips");

  const transcript = await readFile(
    new URL("../src/chat/ChatTranscript.vue", import.meta.url),
    "utf8",
  );
  assert.match(
    transcript,
    /media: message\.media \|\| \[\]/,
    "a recorded message's attachments reach the bubble",
  );
});
