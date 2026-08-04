// Minimal DOM stand-in so the DOM-building primitives in src/base can be
// unit-tested under `node --test` without a browser or a jsdom dependency.
//
// It implements exactly the surface that el/mount/icon/statTile touch:
// createElement/createElementNS, className, setAttribute, addEventListener,
// append/replaceChildren, innerHTML, and enough child traversal for tests to
// assert on rendered output. Nothing here is used at runtime.

class Node {
  constructor() {
    this.childNodes = [];
    this.attrs = {};
    this._innerHTML = "";
  }

  append(...cs) {
    for (const c of cs.flat(Infinity)) {
      if (c == null || c === false) continue;
      this.childNodes.push(c instanceof Node ? c : new TextNode(String(c)));
    }
  }

  replaceChildren(...cs) {
    this.childNodes = [];
    this.append(...cs);
  }

  setAttribute(k, v) {
    this.attrs[k] = String(v);
  }

  getAttribute(k) {
    return this.attrs[k] ?? null;
  }

  addEventListener() {}

  set className(v) {
    this.attrs["class"] = v;
  }

  get className() {
    return this.attrs["class"] ?? "";
  }

  set innerHTML(v) {
    this._innerHTML = v;
  }

  get innerHTML() {
    return this._innerHTML;
  }

  get textContent() {
    return this.childNodes.map((c) => c.textContent).join("");
  }

  get children() {
    return this.childNodes.filter((c) => c instanceof Element);
  }

  /** Class-token lookup, enough for assertions to find .stat-value etc. */
  querySelector(sel) {
    const token = sel.startsWith(".") ? sel.slice(1) : null;
    const want = token || sel;
    const queue = [...this.childNodes];
    while (queue.length) {
      const c = queue.shift();
      if (c instanceof Element) {
        const classes = (c.attrs["class"] || "").split(/\s+/).filter(Boolean);
        if (token ? classes.includes(want) : c.tagName.toLowerCase() === sel) return c;
        queue.push(...c.childNodes);
      }
    }
    return null;
  }
}

class TextNode extends Node {
  constructor(text) {
    super();
    this._text = String(text);
  }

  get textContent() {
    return this._text;
  }
}

class Element extends Node {
  constructor(tag) {
    super();
    this.tagName = tag.toUpperCase();
  }
}

globalThis.Node = Node;
globalThis.Element = Element;
globalThis.TextNode = TextNode;
globalThis.document = {
  createElement: (tag) => new Element(tag),
  createElementNS: () => new Element("svg"),
  createTextNode: (text) => new TextNode(String(text)),
};
