// Minimal DOM stand-in so DOM-building primitives and Preact components
// can be unit-tested under `node --test` without a full browser.

class Node {
  constructor() {
    this.childNodes = [];
    this.attrs = {};
    this._innerHTML = "";
    this.parentNode = null;
    this.style = {};
  }

  appendChild(c) {
    if (c == null || c === false) return c;
    if (c instanceof DocumentFragment) {
      for (const child of [...c.childNodes]) {
        this.appendChild(child);
      }
      return c;
    }
    const node = c instanceof Node ? c : new TextNode(String(c));
    node.parentNode = this;
    this.childNodes.push(node);
    return node;
  }

  insertBefore(newNode, refNode) {
    if (!refNode) return this.appendChild(newNode);
    if (newNode instanceof DocumentFragment) {
      for (const child of [...newNode.childNodes]) {
        this.insertBefore(child, refNode);
      }
      return newNode;
    }
    const node = newNode instanceof Node ? newNode : new TextNode(String(newNode));
    node.parentNode = this;
    const idx = this.childNodes.indexOf(refNode);
    if (idx === -1) {
      this.childNodes.push(node);
    } else {
      this.childNodes.splice(idx, 0, node);
    }
    return node;
  }

  removeChild(child) {
    const idx = this.childNodes.indexOf(child);
    if (idx !== -1) {
      this.childNodes.splice(idx, 1);
      child.parentNode = null;
    }
    return child;
  }

  remove() {
    if (this.parentNode) {
      this.parentNode.removeChild(this);
    }
  }

  append(...cs) {
    for (const c of cs.flat(Infinity)) {
      if (c == null || c === false) continue;
      this.appendChild(c);
    }
  }

  replaceChildren(...cs) {
    for (const child of [...this.childNodes]) {
      this.removeChild(child);
    }
    this.append(...cs);
  }

  setAttribute(k, v) {
    this.attrs[k] = String(v);
  }

  getAttribute(k) {
    return this.attrs[k] ?? null;
  }

  removeAttribute(k) {
    delete this.attrs[k];
  }

  addEventListener() {}
  removeEventListener() {}
  dispatchEvent() { return true; }

  set className(v) {
    this.attrs["class"] = v;
  }

  get className() {
    return this.attrs["class"] ?? "";
  }

  get classList() {
    return {
      add: (...cls) => {
        const set = new Set((this.className || "").split(/\s+/).filter(Boolean));
        cls.forEach((c) => set.add(c));
        this.className = [...set].join(" ");
      },
      remove: (...cls) => {
        const set = new Set((this.className || "").split(/\s+/).filter(Boolean));
        cls.forEach((c) => set.delete(c));
        this.className = [...set].join(" ");
      },
      toggle: (c, force) => {
        const set = new Set((this.className || "").split(/\s+/).filter(Boolean));
        const has = set.has(c);
        const add = force !== undefined ? force : !has;
        if (add) set.add(c);
        else set.delete(c);
        this.className = [...set].join(" ");
        return add;
      },
      contains: (c) => (this.className || "").split(/\s+/).includes(c),
    };
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

  get firstElementChild() {
    return this.children[0] ?? null;
  }

  get lastElementChild() {
    const ch = this.children;
    return ch[ch.length - 1] ?? null;
  }

  get nextSibling() {
    if (!this.parentNode) return null;
    const idx = this.parentNode.childNodes.indexOf(this);
    return idx !== -1 ? this.parentNode.childNodes[idx + 1] ?? null : null;
  }

  /** Selector lookup for assertions to find .chat-topbar, button, etc. */
  querySelector(sel) {
    return this.querySelectorAll(sel)[0] ?? null;
  }

  querySelectorAll(sel) {
    const parts = sel.trim().split(/\s+/);
    let currentNodes = [this];
    for (const part of parts) {
      const nextNodes = [];
      const isClass = part.startsWith(".");
      const tag = !isClass ? part.toLowerCase() : null;
      const className = isClass ? part.slice(1) : null;

      for (const parent of currentNodes) {
        const queue = [...parent.childNodes];
        while (queue.length) {
          const c = queue.shift();
          if (c instanceof Element) {
            let match = false;
            if (isClass) {
              const classes = (c.attrs["class"] || "").split(/\s+/).filter(Boolean);
              if (classes.includes(className)) match = true;
            } else if (tag && c.tagName.toLowerCase() === tag) {
              match = true;
            }
            if (match) nextNodes.push(c);
            queue.push(...c.childNodes);
          }
        }
      }
      currentNodes = nextNodes;
    }
    return currentNodes;
  }
}

class TextNode extends Node {
  constructor(text) {
    super();
    this._text = String(text);
    this.nodeType = 3;
  }

  get textContent() {
    return this._text;
  }

  set textContent(v) {
    this._text = String(v);
  }

  get data() {
    return this._text;
  }

  set data(v) {
    this._text = String(v);
  }
}

class DocumentFragment extends Node {
  constructor() {
    super();
    this.nodeType = 11;
  }
}

class Element extends Node {
  constructor(tag) {
    super();
    this.tagName = tag.toUpperCase();
    this.nodeType = 1;
    this.dataset = {};
  }
}

globalThis.Node = Node;
globalThis.Element = Element;
globalThis.TextNode = TextNode;
globalThis.DocumentFragment = DocumentFragment;
globalThis.document = {
  createElement: (tag) => new Element(tag),
  createElementNS: (ns, tag) => new Element(tag || "svg"),
  createDocumentFragment: () => new DocumentFragment(),
  createTextNode: (text) => new TextNode(String(text)),
};
globalThis.window = globalThis;
globalThis.addEventListener = () => {};
globalThis.removeEventListener = () => {};
