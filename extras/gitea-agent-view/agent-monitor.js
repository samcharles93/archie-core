// Copy to: $GITEA_CUSTOM/public/assets/agent-monitor.js
// Referenced from body_inner_post.tmpl via
//   <script src="{{AppSubUrl}}/assets/agent-monitor.js" data-repo="{{.Repository.FullName}}">
// rather than an inline <script> block — Gitea's CSP header requires a
// per-request nonce for inline scripts that custom/templates has no way
// to supply, but external script sources match the "*" already present
// in script-src, so this loads fine with no CSP changes needed.
//
// Doesn't hide or duplicate list.tmpl's sidebar — that's real,
// always-current Gitea markup. Instead it swaps the CONTENT of the
// existing ".ui.attached.segment" (the element list.tmpl already sizes
// correctly to hold the runs list) for the terminal. Nesting the
// terminal inside other stock wrapper classes instead (e.g.
// ".ui.top.attached.header", meant for the slim run-count bar) is what
// produced a tiny clipped box in earlier testing — this element is the
// right one.
(function () {
  const scriptTag = document.currentScript;
  const repoFilter = scriptTag ? scriptTag.dataset.repo : "";
  console.log("[agent-monitor] loaded, repo=", repoFilter);

  // Primary selector matches list.tmpl's structure; fall back to a looser
  // one (drop the .flex-container-main ancestor requirement) and finally
  // to "last .ui.attached.segment on the page" in case a workflow_dispatch
  // block or markup drift shifted things — log which one hit so a failed
  // match is visible in the console instead of silently doing nothing.
  const candidates = [
    ".repository.actions .flex-container-main .ui.attached.segment",
    ".repository.actions .ui.attached.segment",
    ".ui.attached.segment",
  ];
  let segment = null;
  for (const sel of candidates) {
    const found = document.querySelectorAll(sel);
    if (found.length) {
      segment = found[found.length - 1]; // last match = likely the runs-list one
      console.log("[agent-monitor] matched selector:", sel, "(", found.length, "candidate(s))");
      break;
    }
  }
  if (!segment) {
    console.warn("[agent-monitor] no .ui.attached.segment found on page — markup shape changed, nothing to swap");
    return;
  }

  // The run-count/filter bar sits directly above the segment; hide it,
  // there's nothing meaningful to filter once we're showing the terminal.
  const counterBar = segment.previousElementSibling;
  if (counterBar && counterBar.classList.contains("flex-left-right")) {
    counterBar.style.display = "none";
  }

  segment.innerHTML = "";
  Object.assign(segment.style, {
    padding: "12px",
    borderRadius: "10px",
    overflow: "hidden",
    boxShadow: "0 2px 10px rgba(0,0,0,0.35)",
    background: "#1e1e1e",
    border: "none",
    height: "70vh",
    boxSizing: "border-box",
  });

  const el = document.createElement("div");
  Object.assign(el.style, { height: "100%", width: "100%" });
  segment.appendChild(el);

  // TODO: point this at wherever internal/webui (archie-core's SSE
  // dashboard) is reachable from the browser — reverse-proxy it under
  // this same host to avoid CORS. Override per-load with ?webui=.
  const WEBUI_BASE = new URLSearchParams(location.search).get("webui")
    || "https://gitea.example.com/agent-events";

  const term = new Terminal({ convertEol: true, disableStdin: true, cursorBlink: false, fontSize: 13 });
  term.open(el);

  // FitAddon sizes xterm's internal viewport (rows/cols) to match the
  // container's actual pixel size — without it xterm falls back to a
  // default 80x24 grid that doesn't match our div, which is what made
  // the terminal look mismatched/"out of place" against the card.
  if (window.FitAddon) {
    const fit = new window.FitAddon.FitAddon();
    term.loadAddon(fit);
    fit.fit();
    window.addEventListener("resize", () => fit.fit());
  }

  term.writeln(`\x1b[90mconnecting to ${WEBUI_BASE} (repo=${repoFilter})...\x1b[0m`);

  function colorFor(stage) {
    switch (stage) {
      case "error": return "\x1b[31m";
      case "done":  return "\x1b[32m";
      default:      return "\x1b[36m";
    }
  }

  function render(e) {
    if (repoFilter && e.repo && e.repo !== repoFilter) return;
    const ts = new Date().toLocaleTimeString();
    const tag = [e.workflow, e.stage].filter(Boolean).join(":");
    term.writeln(`\x1b[90m${ts}\x1b[0m ${colorFor(e.stage)}[${tag || "event"}]\x1b[0m ${e.detail || ""}`);
    if (e.data && Object.keys(e.data).length) {
      term.writeln(`\x1b[90m  ${JSON.stringify(e.data)}\x1b[0m`);
    }
  }

  // A new line per failed attempt spams the screen when the endpoint is
  // unreachable for a while (each retry is 3s apart, but it adds up).
  // Print the "lost" message once per disconnect, then just tick dots on
  // the same line so it still reads as "trying," not a wall of red text.
  let retrying = false;
  function connect() {
    const es = new EventSource(WEBUI_BASE + "/events");
    es.onopen = () => {
      if (retrying) term.writeln("\x1b[32m connected\x1b[0m");
      retrying = false;
    };
    es.onmessage = (msg) => { try { render(JSON.parse(msg.data)); } catch {} };
    es.onerror = () => {
      es.close();
      if (!retrying) {
        term.writeln("\x1b[31mconnection lost, retrying...\x1b[0m");
        retrying = true;
      } else {
        term.write("\x1b[31m.\x1b[0m");
      }
      setTimeout(connect, 3000);
    };
  }
  connect();
})();
