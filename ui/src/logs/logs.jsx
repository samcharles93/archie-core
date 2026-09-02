import { h } from "preact";
import { useState, useEffect, useRef, useMemo } from "preact/hooks";
import "./logs.css";
import { api, subscribeLogs } from "../base/api.jsx";

function Pill({ text, kind = "idle" }) {
  return <span className={`pill pill-${kind}`}>{text}</span>;
}

function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

export function levelKind(level) {
  switch ((level || "").toUpperCase()) {
    case "ERROR": return "danger";
    case "WARN": return "warn";
    case "DEBUG": return "idle";
    default: return "info";
  }
}

export function fmtValue(v) {
  if (v == null) return "";
  return typeof v === "object" ? JSON.stringify(v) : v;
}

export function shortTime(value) {
  const d = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(d.getTime())) return "--:--:--";
  return d.toTimeString().slice(0, 8);
}

function LogRow({ entry }) {
  const fields = entry.fields || {};
  return (
    <div className={`log-row log-${levelKind(entry.level)}`}>
      <span className="log-time mono">{shortTime(entry.time)}</span>
      <span className="log-level">{(entry.level || "info").toUpperCase()}</span>
      <span className="log-body">
        <span className="log-msg">{entry.message || entry.msg || ""}</span>
        {Object.entries(fields).map(([k, v]) => (
          <span className="log-field" key={k}>
            <span className="log-field-key">{k}</span>
            {String(fmtValue(v))}
          </span>
        ))}
      </span>
    </div>
  );
}

const LEVELS = [
  { value: "", label: "All levels" },
  { value: "ERROR", label: "Errors" },
  { value: "WARN,ERROR", label: "Warnings and errors" },
  { value: "INFO,WARN,ERROR", label: "Info and above" },
  { value: "DEBUG", label: "Debug only" },
];

function entryKey(entry) {
	return `${entry.time || ""}|${entry.level || ""}|${entry.message || entry.msg || ""}|${JSON.stringify(entry.fields || {})}`;
}

function LogsApp() {
  const [filters, setFilters] = useState({ level: "", component: "", q: "" });
  const [paused, setPaused] = useState(false);
  const [componentOptions, setComponentOptions] = useState([]);
  const [historyEntries, setHistoryEntries] = useState([]);
  const liveEntriesRef = useRef(new Map());
  const [liveEntriesCounter, setLiveEntriesCounter] = useState(0);
  const [durableUnavailable, setDurableUnavailable] = useState(false);
  const [streamState, setStreamState] = useState("connecting");
  const [meta, setMeta] = useState("Refreshing…");
  const listRef = useRef(null);
  const searchTimeout = useRef(null);

  const load = async () => {
    setMeta("Refreshing…");
    try {
      const res = await api.logs({
        level: filters.level,
        component: filters.component,
        q: filters.q,
        limit: 500,
      });

      setComponentOptions(prev => {
        const newComps = res.components || [];
        if (prev.length === newComps.length && prev.every((v, i) => v === newComps[i])) return prev;
        return newComps;
      });
      setDurableUnavailable(!!res.disabled);
      
      const newMeta = res.disabled ? "Durable history unavailable; live daemon logs continue." : res.truncated
        ? `showing the most recent matches from ${res.file}`
        : res.file || "";
      setMeta(newMeta);
      setHistoryEntries(res.entries || []);
    } catch (err) {
      setMeta("Cannot read logs");
    }
  };

  useEffect(() => {
    load();
  }, [filters]);

  useEffect(() => {
    const handleEvent = (event) => {
      if (pausedRef.current) return;
      const entry = event;
      if (!matchesLocallyRef.current(entry)) return;
      
      liveEntriesRef.current.set(entryKey(entry), entry);
      if (entry.fields?.component) {
        setComponentOptions(prev => {
          if (!prev.includes(entry.fields.component)) return [...prev, entry.fields.component];
          return prev;
        });
      }
      setLiveEntriesCounter(c => c + 1);
    };

    const unsubscribe = subscribeLogs(
      handleEvent,
      (state) => {
        setStreamState(state);
      }
    );
    
    return () => unsubscribe();
  }, []); // Only run once

  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  const matchesLocally = (entry) => {
    if (filters.level && !filters.level.split(",").includes((entry.level || "").toUpperCase())) {
      return false;
    }
    if (filters.component && entry.fields?.component !== filters.component) return false;
    if (filters.q) {
      const needle = filters.q.toLowerCase();
      const hay = `${entry.message || entry.msg || ""} ${JSON.stringify(entry.fields || {})}`.toLowerCase();
      if (!hay.includes(needle)) return false;
    }
    return true;
  };
  const matchesLocallyRef = useRef(matchesLocally);
  matchesLocallyRef.current = matchesLocally;

  const entriesToRender = useMemo(() => {
    const byID = new Map();
    for (const entry of historyEntries) byID.set(entryKey(entry), entry);
    for (const [id, entry] of liveEntriesRef.current) byID.set(id, entry);
    return [...byID.values()].filter(matchesLocally).slice(-1000);
  }, [historyEntries, liveEntriesCounter, filters]);

  useEffect(() => {
    if (listRef.current) {
      listRef.current.scrollTop = listRef.current.scrollHeight;
    }
  }, [entriesToRender]);

  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Logs</h1>
          <p className="page-sub">What Archie has been doing, newest last.</p>
        </div>
        <div className="page-actions">
          <button 
            className={`btn ${paused ? "btn-primary" : ""}`} 
            onClick={() => setPaused(!paused)}
          >
            {paused ? "Resume" : "Pause"}
          </button>
          <button className="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      
      <div className="card">
        <div className="card-head">
          <div className="log-filters">
            <select 
              className="log-select" 
              value={filters.level} 
              onChange={(e) => setFilters(f => ({ ...f, level: e.target.value }))}
            >
              {LEVELS.map(l => <option key={l.value} value={l.value}>{l.label}</option>)}
            </select>
            <select 
              className="log-select" 
              value={filters.component} 
              onChange={(e) => setFilters(f => ({ ...f, component: e.target.value }))}
            >
              <option value="">All components</option>
              {componentOptions.map(c => <option key={c} value={c}>{c}</option>)}
            </select>
            <input 
              className="log-search" 
              type="search" 
              placeholder="Search messages and fields…" 
              onInput={(e) => {
                const v = e.target.value;
                if (searchTimeout.current) clearTimeout(searchTimeout.current);
                searchTimeout.current = setTimeout(() => {
                  setFilters(f => ({ ...f, q: v }));
                }, 250);
              }} 
            />
          </div>
          <div className="log-status">
            <span className="log-meta">{meta}</span>
            <Pill text={streamState} kind={streamState === "live" ? "ok" : "warn"} />
          </div>
        </div>
        
        <div className="log-list" ref={listRef}>
          {meta === "Cannot read logs" ? (
            <Empty title="Cannot read logs" />
          ) : !entriesToRender.length ? (
            <Empty 
              title={durableUnavailable ? "Durable history unavailable" : "Nothing matches"} 
              detail={durableUnavailable ? "Live daemon logs will appear here while this page is open." : "Try a wider level or clear the search."} 
            />
          ) : (
            entriesToRender.map(entry => <LogRow key={entryKey(entry)} entry={entry} />)
          )}
        </div>
      </div>
    </div>
  );
}

export function logsPage(query) {
  return <LogsApp query={query} />;
}
