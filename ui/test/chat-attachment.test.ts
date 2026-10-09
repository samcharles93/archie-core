import assert from "node:assert/strict";
import test from "node:test";

import { mediaDetail, mediaLabel, type ChatMedia } from "../src/chat/attachment.ts";

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
