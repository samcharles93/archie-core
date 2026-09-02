import { h, Fragment } from "preact";
import { useState, useEffect, useMemo } from "preact/hooks";
import "./skills.css";
import { api } from "../base/api.jsx";
import { Pill } from "../base/pill.jsx";

function Empty({ title, detail }) {
  return (
    <div class="empty">
      <div class="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

export function skillsPage(query) {
  return <SkillsApp query={query} />;
}

function SkillsApp() {
  const [skills, setSkills] = useState([]);
  const [search, setSearch] = useState("");
  const [loadError, setLoadError] = useState(null);

  const load = async () => {
    try {
      const res = await api.skills();
      setSkills(res?.skills || []);
      setLoadError(null);
    } catch (err) {
      setSkills([]);
      setLoadError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  const matches = (skill, term) => {
    if (!term) return true;
    const haystack = `${skill.name} ${skill.description}`.toLowerCase();
    return haystack.includes(term);
  };

  const filteredSkills = useMemo(() => skills.filter((s) => matches(s, search)), [skills, search]);

  return (
    <div>
      <div class="page-head">
        <div>
          <h1 class="page-title">Skills</h1>
          <p class="page-sub">What Archie can do, in plain language.</p>
        </div>
        <div class="page-actions">
          <button class="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      <div class="card">
        <div class="card-head">
          <div>
            <h2 class="card-title">Catalogue</h2>
            <p class="card-sub">Project, shared, and user-global skills</p>
          </div>
          <input
            class="skill-search"
            type="search"
            placeholder="Search skills…"
            onInput={(e) => setSearch(e.target.value.trim().toLowerCase())}
          />
        </div>
        <div class="grid grid-2">
          {loadError ? (
            <Empty title="Cannot reach archied" detail={loadError} />
          ) : !skills.length ? (
            <Empty
              title="No skills discovered yet"
              detail="Skills live as SKILL.md files under project, shared, or user-global .agents/skills/<name>/ directories. Add one and refresh to make it available."
            />
          ) : !filteredSkills.length ? (
            <Empty title="No skills match" detail={`Nothing found for "${search}".`} />
          ) : (
            filteredSkills.map((skill) => (
              <div class="card skill-card" key={skill.name}>
                <div class="card-head">
                  <h3 class="card-title">{skill.name || "Untitled skill"}</h3>
                  {skill.workflow && <Pill text={skill.workflow} kind="info" />}
                </div>
                <p class="skill-desc">{skill.description || "No description provided."}</p>
                <div class="skill-source">{skill.source || "Unknown source"}</div>
              </div>
            ))
          )}
        </div>
      </div>
    </div>
  );
}
