/**
 * Chat markdown.
 *
 * Telegram-style rich blocks -- headings, lists, quotes, fenced code, tables,
 * inline emphasis/links/images -- parsed into a tree that ChatMarkdown renders
 * as semantic HTML. The parse is separate from the render so a streamed reply
 * can be re-rendered on every frame without re-deciding what the text means.
 */

/** One span of an inline run: what a paragraph's text breaks down into. */
export type ChatInline =
  | { kind: "text"; text: string }
  | { kind: "strong"; text: string }
  | { kind: "em"; text: string }
  | { kind: "del"; text: string }
  | { kind: "code"; text: string }
  | { kind: "link"; text: string; href: string }
  | { kind: "image"; alt: string; src: string };

/** One block of the document. Lists carry their items as inline runs. */
export type ChatBlock =
  | { kind: "p" | "h1" | "h2" | "h3" | "blockquote"; inline: ChatInline[] }
  | { kind: "ul" | "ol"; items: ChatInline[][] }
  | { kind: "pre"; code: string }
  | { kind: "table"; head: ChatInline[][]; rows: ChatInline[][][] };

const INLINE =
  /(!\[[^\]]*\]\((?:https?:\/\/[^\s)]+|data:image\/[^\s)]+|\/[^\s)]+)\)|~~[^~]+~~|\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|\[[^\]]+\]\((?:https?:\/\/[^\s)]+|\/[^\s)]+)\)|\*[^*]+\*|_[^_]+_)/g;

// inlineMarkdown returns the children of one line: plain text and the element
// each matched span becomes, in order.
function inlineMarkdown(text: string): ChatInline[] {
  const children: ChatInline[] = [];
  let cursor = 0;
  for (const match of String(text).matchAll(INLINE)) {
    if (match.index > cursor) children.push({ kind: "text", text: text.slice(cursor, match.index) });
    const value = match[0];
    if (value.startsWith("![")) {
      const img = value.match(/^!\[([^\]]*)\]\((.+)\)$/);
      if (img) children.push({ kind: "image", alt: img[1] || "Image", src: img[2] });
    } else if (value.startsWith("~~")) {
      children.push({ kind: "del", text: value.slice(2, -2) });
    } else if (value.startsWith("**") || value.startsWith("__")) {
      children.push({ kind: "strong", text: value.slice(2, -2) });
    } else if (value.startsWith("`")) {
      children.push({ kind: "code", text: value.slice(1, -1) });
    } else if (value.startsWith("[")) {
      const link = value.match(/^\[([^\]]+)\]\((https?:\/\/[^\s)]+|\/[^\s)]+)\)$/);
      if (link) children.push({ kind: "link", text: link[1], href: link[2] });
    } else {
      children.push({ kind: "em", text: value.slice(1, -1) });
    }
    cursor = match.index + value.length;
  }
  if (cursor < text.length) children.push({ kind: "text", text: text.slice(cursor) });
  return children;
}

export function parseMarkdown(text: string | null | undefined): ChatBlock[] {
  const blocks: ChatBlock[] = [];
  const lines = String(text || "").split("\n");
  let paragraph: string[] = [];
  let list: { tag: "ul" | "ol"; items: string[] } | null = null;
  let code: string[] | null = null;

  const flushParagraph = () => {
    if (paragraph.length) {
      blocks.push({ kind: "p", inline: inlineMarkdown(paragraph.join(" ")) });
      paragraph = [];
    }
  };
  const flushList = () => {
    if (!list) return;
    blocks.push({ kind: list.tag, items: list.items.map((item) => inlineMarkdown(item)) });
    list = null;
  };

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (line.startsWith("```")) {
      flushParagraph();
      flushList();
      if (code) {
        blocks.push({ kind: "pre", code: code.join("\n") });
        code = null;
      } else {
        code = [];
      }
      continue;
    }
    if (code) {
      code.push(line);
      continue;
    }
    const headerCells = tableCells(line);
    const separatorCells = index + 1 < lines.length ? tableCells(lines[index + 1]) : null;
    if (
      headerCells &&
      separatorCells &&
      isTableSeparator(separatorCells) &&
      headerCells.length === separatorCells.length
    ) {
      flushParagraph();
      flushList();
      const rows: string[][] = [];
      for (index += 2; index < lines.length; index++) {
        const cells = tableCells(lines[index]);
        if (!cells || cells.length !== headerCells.length) {
          index--;
          break;
        }
        rows.push(cells);
      }
      blocks.push({
        kind: "table",
        head: headerCells.map((cell) => inlineMarkdown(cell)),
        rows: rows.map((row) => row.map((cell) => inlineMarkdown(cell))),
      });
      continue;
    }
    if (!line.trim()) {
      flushParagraph();
      flushList();
      continue;
    }
    const heading = line.match(/^(#{1,3})\s+(.+)$/);
    if (heading) {
      flushParagraph();
      flushList();
      blocks.push({ kind: `h${heading[1].length}` as "h1" | "h2" | "h3", inline: inlineMarkdown(heading[2]) });
      continue;
    }
    const bullet = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (bullet || ordered) {
      flushParagraph();
      const tag = ordered ? "ol" : "ul";
      if (!list || list.tag !== tag) {
        flushList();
        list = { tag, items: [] };
      }
      list.items.push((bullet || ordered)![1]);
      continue;
    }
    if (line.startsWith(">")) {
      flushParagraph();
      flushList();
      blocks.push({ kind: "blockquote", inline: inlineMarkdown(line.replace(/^>\s?/, "")) });
      continue;
    }
    paragraph.push(line);
  }
  flushParagraph();
  flushList();
  if (code) blocks.push({ kind: "pre", code: code.join("\n") });

  return blocks;
}

function tableCells(line: string): string[] | null {
  const value = String(line || "").trim();
  if (!value.includes("|")) return null;
  const unwrapped = value.replace(/^\|/, "").replace(/\|$/, "");
  const cells = unwrapped.split("|").map((cell) => cell.trim());
  return cells.length > 1 && cells.every((cell) => cell !== "") ? cells : null;
}

function isTableSeparator(cells: string[]): boolean {
  return cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}
