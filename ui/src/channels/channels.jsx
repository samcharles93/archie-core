import { h } from "preact";
import { useState, useEffect } from "preact/hooks";
import "./channels.css";
import { api } from "../base/api.jsx";
import { Pill } from "../base/pill.jsx";

export function Empty({ title, detail }) {
  return (
    <div className="empty">
      <div className="empty-title">{title}</div>
      {detail && <div>{detail}</div>}
    </div>
  );
}

function stateTone(state, configured) {
  if (state === "running") return "ok";
  if (state === "failed" || state === "degraded") return "warn";
  return configured ? "idle" : "idle";
}

function ChannelsApp() {
  const [channels, setChannels] = useState([]);
  const [error, setError] = useState(null);

  const load = async () => {
    try {
      const res = await api.channels();
      setChannels(res?.channels || []);
      setError(null);
    } catch (err) {
      setError(String(err.message || err));
    }
  };

  useEffect(() => {
    load();
  }, []);

  return (
    <div>
      <div className="page-head">
        <div>
          <h1 className="page-title">Channels</h1>
          <p className="page-sub">The conversational front-ends Archie can be reached through.</p>
        </div>
        <div className="page-actions">
          <button className="btn" onClick={load}>Refresh</button>
        </div>
      </div>
      <div className="grid grid-2">
        {error ? (
          <Empty title="Cannot reach archied" detail={error} />
        ) : channels.length === 0 ? (
          <Empty 
            title="No channels configured" 
            detail="Configure [chat.telegram] or [chat.webhook_addr] in config.toml to talk to Archie outside the dashboard." 
          />
        ) : (
          channels.map(channel => (
            <div className="card channel-card" key={channel.id || channel.name}>
              <div className="card-head">
                <h3 className="card-title">{channel.name}</h3>
                <Pill 
                  text={channel.state || (channel.configured ? "configured" : "stopped")} 
                  kind={stateTone(channel.state, channel.configured)} 
                />
              </div>
              <p className="channel-desc">{channel.description}</p>
              <p className="channel-detail">{channel.detail}</p>
              {channel.reload_supported ? (
                <button 
                  className="btn" 
                  onClick={async (event) => { 
                    event.currentTarget.disabled = true; 
                    try { 
                      await api.channelReload(channel.id || channel.name.toLowerCase()); 
                      await load(); 
                    } catch (err) {
                      // skip
                    } finally {
                      if (event.currentTarget) event.currentTarget.disabled = false;
                    } 
                  }}
                >
                  Reload
                </button>
              ) : (
                <p className="channel-detail">Reload requires a daemon restart for this adapter.</p>
              )}
            </div>
          ))
        )}
      </div>
    </div>
  );
}

export function channelsPage(query) {
    return <ChannelsApp query={query} />;
}
