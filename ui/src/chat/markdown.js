import { el } from "../base/dom.js";

function inlineMarkdown(text) {
  const fragment = document.createDocumentFragment();
  const pattern = /(~~[^~]+~~|\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|\[[^\]]+\]\(https?:\/\/[^\s)]+\)|\*[^*]+\*|_[^_]+_)/g;
  let cursor = 0;
  for (const match of text.matchAll(pattern)) {
    if (match.index > cursor) fragment.append(document.createTextNode(text.slice(cursor, match.index)));
    const value = match[0];
    if (value.startsWith("~~")) fragment.append(el("del", value.slice(2, -2)));
    else if (value.startsWith("**") || value.startsWith("__")) fragment.append(el("strong", value.slice(2, -2)));
    else if (value.startsWith("`")) fragment.append(el("code", value.slice(1, -1)));
    else if (value.startsWith("[")) {
      const link = value.match(/^\[([^\]]+)\]\((https?:\/\/[^\s)]+)\)$/);
      fragment.append(el("a", { href: link[2], target: "_blank", rel: "noreferrer" }, link[1]));
    } else fragment.append(el("em", value.slice(1, -1)));
    cursor = match.index + value.length;
  }
  if (cursor < text.length) fragment.append(document.createTextNode(text.slice(cursor)));
  return fragment;
}

export function renderMarkdown(text) {
  const root = el("div.chat-bubble-text");
  const lines = String(text || "").split("\n");
  let paragraph = [];
  let list = null;
  let code = null;
  const flushParagraph = () => {
    if (paragraph.length) {
      root.append(el("p", inlineMarkdown(paragraph.join(" "))));
      paragraph = [];
    }
  };
  const flushList = () => {
    if (list) root.append(list);
    list = null;
  };
  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (line.startsWith("```")) {
      flushParagraph();
      flushList();
      if (code) {
        root.append(el("pre", el("code", code.join("\n"))));
        code = null;
      } else code = [];
      continue;
    }
    if (code) {
      code.push(line);
      continue;
    }
    const headerCells = tableCells(line);
    const separatorCells = index + 1 < lines.length ? tableCells(lines[index + 1]) : null;
    if (headerCells && separatorCells && isTableSeparator(separatorCells) && headerCells.length === separatorCells.length) {
      flushParagraph();
      flushList();
      const rows = [];
      for (index += 2; index < lines.length; index++) {
        const cells = tableCells(lines[index]);
        if (!cells || cells.length !== headerCells.length) {
          index--;
          break;
        }
        rows.push(cells);
      }
      const body = el("tbody");
      for (const row of rows) body.append(el("tr", ...row.map((cell) => el("td", inlineMarkdown(cell)))));
      root.append(el("table", el("thead", el("tr", ...headerCells.map((cell) => el("th", inlineMarkdown(cell))))), body));
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
      root.append(el(`h${heading[1].length}`, inlineMarkdown(heading[2])));
      continue;
    }
    const bullet = line.match(/^\s*[-*]\s+(.+)$/);
    const ordered = line.match(/^\s*\d+[.)]\s+(.+)$/);
    if (bullet || ordered) {
      flushParagraph();
      const tag = ordered ? "ol" : "ul";
      if (!list || list.tagName.toLowerCase() !== tag) {
        flushList();
        list = el(tag);
      }
      list.append(el("li", inlineMarkdown((bullet || ordered)[1])));
      continue;
    }
    if (line.startsWith(">")) {
      flushParagraph();
      flushList();
      root.append(el("blockquote", inlineMarkdown(line.replace(/^>\s?/, ""))));
      continue;
    }
    paragraph.push(line);
  }
  flushParagraph();
  flushList();
  if (code) root.append(el("pre", el("code", code.join("\n"))));
  return root;
}

function tableCells(line) {
  const value = String(line || "").trim();
  if (!value.includes("|")) return null;
  const unwrapped = value.replace(/^\|/, "").replace(/\|$/, "");
  const cells = unwrapped.split("|").map((cell) => cell.trim());
  return cells.length > 1 && cells.every((cell) => cell !== "") ? cells : null;
}

function isTableSeparator(cells) {
  return cells.every((cell) => /^:?-{3,}:?$/.test(cell));
}
