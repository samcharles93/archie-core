import { h } from "preact";

/**
 * Chat markdown.
 *
 * Telegram-style rich blocks -- headings, lists, quotes, fenced code, tables,
 * inline emphasis/links/images -- rendered as semantic HTML. It is a
 * component rather than a DOM builder so a streamed reply can be diffed: the
 * transcript re-renders on every frame, and diffing is what stops the whole
 * bubble being rebuilt each time the answer grows.
 */

const INLINE =
  /(!\[[^\]]*\]\((?:https?:\/\/[^\s)]+|data:image\/[^\s)]+|\/[^\s)]+)\)|~~[^~]+~~|\*\*[^*]+\*\*|__[^_]+__|`[^`]+`|\[[^\]]+\]\((?:https?:\/\/[^\s)]+|\/[^\s)]+)\)|\*[^*]+\*|_[^_]+_)/g;

// inlineMarkdown returns the children of one line: plain text and the element
// each matched span becomes, in order.
function inlineMarkdown(text) {
  const children = [];
  let cursor = 0;
  let key = 0;
  for (const match of String(text).matchAll(INLINE)) {
    if (match.index > cursor) children.push(text.slice(cursor, match.index));
    const value = match[0];
    if (value.startsWith("![")) {
      const img = value.match(/^!\[([^\]]*)\]\((.+)\)$/);
      if (img) {
        children.push(
          <img className="chat-media-img" src={img[2]} alt={img[1] || "Image"} loading="lazy" key={key++} />,
        );
      }
    } else if (value.startsWith("~~")) {
      children.push(<del key={key++}>{value.slice(2, -2)}</del>);
    } else if (value.startsWith("**") || value.startsWith("__")) {
      children.push(<strong key={key++}>{value.slice(2, -2)}</strong>);
    } else if (value.startsWith("`")) {
      children.push(<code key={key++}>{value.slice(1, -1)}</code>);
    } else if (value.startsWith("[")) {
      const link = value.match(/^\[([^\]]+)\]\((https?:\/\/[^\s)]+|\/[^\s)]+)\)$/);
      if (link) {
        children.push(
          <a href={link[2]} target="_blank" rel="noreferrer" key={key++}>
            {link[1]}
          </a>,
        );
      }
    } else {
      children.push(<em key={key++}>{value.slice(1, -1)}</em>);
    }
    cursor = match.index + value.length;
  }
  if (cursor < text.length) children.push(text.slice(cursor));
  return children;
}

export function ChatMarkdown({ text, className = "chat-bubble-text" }) {
  const blocks = [];
  const lines = String(text || "").split("\n");
  let paragraph = [];
  let list = null;
  let code = null;
  let key = 0;

  const flushParagraph = () => {
    if (paragraph.length) {
      blocks.push(<p key={key++}>{inlineMarkdown(paragraph.join(" "))}</p>);
      paragraph = [];
    }
  };
  const flushList = () => {
    if (!list) return;
    const items = list.items.map((item, i) => <li key={i}>{inlineMarkdown(item)}</li>);
    blocks.push(list.tag === "ol" ? <ol key={key++}>{items}</ol> : <ul key={key++}>{items}</ul>);
    list = null;
  };

  for (let index = 0; index < lines.length; index++) {
    const line = lines[index];
    if (line.startsWith("```")) {
      flushParagraph();
      flushList();
      if (code) {
        blocks.push(
          <pre key={key++}>
            <code>{code.join("\n")}</code>
          </pre>,
        );
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
      const rows = [];
      for (index += 2; index < lines.length; index++) {
        const cells = tableCells(lines[index]);
        if (!cells || cells.length !== headerCells.length) {
          index--;
          break;
        }
        rows.push(cells);
      }
      blocks.push(
        <table key={key++}>
          <thead>
            <tr>
              {headerCells.map((cell, i) => (
                <th key={i}>{inlineMarkdown(cell)}</th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, r) => (
              <tr key={r}>
                {row.map((cell, c) => (
                  <td key={c}>{inlineMarkdown(cell)}</td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>,
      );
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
      const Tag = `h${heading[1].length}`;
      blocks.push(<Tag key={key++}>{inlineMarkdown(heading[2])}</Tag>);
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
      list.items.push((bullet || ordered)[1]);
      continue;
    }
    if (line.startsWith(">")) {
      flushParagraph();
      flushList();
      blocks.push(<blockquote key={key++}>{inlineMarkdown(line.replace(/^>\s?/, ""))}</blockquote>);
      continue;
    }
    paragraph.push(line);
  }
  flushParagraph();
  flushList();
  if (code) {
    blocks.push(
      <pre key={key++}>
        <code>{code.join("\n")}</code>
      </pre>,
    );
  }

  return <div className={className}>{blocks}</div>;
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
