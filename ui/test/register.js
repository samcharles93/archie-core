import { register } from "node:module";
import { JSDOM } from "jsdom";

register("./hooks.js", import.meta.url);

// A real DOM for @testing-library/preact. The custom shim in shim.js covers
// only the el() DOM-building primitives; the testing library needs full DOM
// globals (document, window, getComputedStyle, MutationObserver, ...), which
// jsdom provides. Applied here so every test file sees them before importing
// source modules.
const dom = new JSDOM("<!doctype html><html><body></body></html>", { url: "http://localhost/" });
const { window } = dom;

// Some globals (navigator, location) are read-only accessors on Node's
// globalThis, so assign via defineProperty rather than direct assignment.
const setGlobal = (key, value) => {
  Object.defineProperty(globalThis, key, { value, configurable: true, writable: true });
};

setGlobal("window", window);
setGlobal("document", window.document);
setGlobal("navigator", window.navigator);
setGlobal("HTMLElement", window.HTMLElement);
setGlobal("Element", window.Element);
setGlobal("Node", window.Node);
setGlobal("Text", window.Text);
setGlobal("TextNode", window.TextNode);
setGlobal("DocumentFragment", window.DocumentFragment);
setGlobal("CustomEvent", window.CustomEvent);
setGlobal("Event", window.Event);
setGlobal("AbortController", window.AbortController);
setGlobal("getComputedStyle", window.getComputedStyle.bind(window));
setGlobal("MutationObserver", window.MutationObserver);
if (typeof window.requestAnimationFrame === "function") {
  setGlobal("requestAnimationFrame", window.requestAnimationFrame.bind(window));
} else {
  setGlobal("requestAnimationFrame", (cb) => setTimeout(() => cb(Date.now()), 0));
}
if (typeof window.cancelAnimationFrame === "function") {
  setGlobal("cancelAnimationFrame", window.cancelAnimationFrame.bind(window));
} else {
  setGlobal("cancelAnimationFrame", (id) => clearTimeout(id));
}
setGlobal("localStorage", window.localStorage);
setGlobal("location", window.location);
