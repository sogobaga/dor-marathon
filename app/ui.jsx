/* DOR app — shared UI primitives + icon set (exported to window) */

/* ---- simple geometric icon set (stroke-based) ---- */
function Icon({ name, size = 24, color = 'currentColor', sw = 1.8, fill = 'none' }) {
  const p = { fill: 'none', stroke: color, strokeWidth: sw, strokeLinecap: 'round', strokeLinejoin: 'round' };
  const F = { fill: color, stroke: 'none' };
  const paths = {
    target: <g {...p}><circle cx="12" cy="12" r="8.5"/><circle cx="12" cy="12" r="4"/><circle cx="12" cy="12" r="1" {...F}/></g>,
    flag: <g {...p}><path d="M5 21V4"/><path d="M5 4.5C8 2.5 12 6 18 4v9c-6 2-10-1.5-13 .5"/></g>,
    wheel: <g {...p}><circle cx="12" cy="12" r="8.5"/><path d="M12 3.5v17M3.5 12h17M6 6l12 12M18 6L6 18"/><circle cx="12" cy="12" r="1.4" {...F}/></g>,
    user: <g {...p}><circle cx="12" cy="8" r="4"/><path d="M4.5 20a7.5 7.5 0 0 1 15 0"/></g>,
    back: <g {...p}><path d="M15 5l-7 7 7 7"/></g>,
    chev: <g {...p}><path d="M9 5l7 7-7 7"/></g>,
    chevDown: <g {...p}><path d="M5 9l7 7 7-7"/></g>,
    check: <g {...p}><path d="M4 12.5l5 5 11-11"/></g>,
    pin: <g {...p}><path d="M12 21s7-6.5 7-12a7 7 0 1 0-14 0c0 5.5 7 12 7 12Z"/><circle cx="12" cy="9" r="2.5"/></g>,
    bolt: <g><path d="M13 2L4 14h7l-1 8 9-12h-7l1-8Z" {...F}/></g>,
    stealth: <g {...p}><path d="M2 12s4-6 10-6 10 6 10 6-4 6-10 6S2 12 2 12Z"/><circle cx="12" cy="12" r="2.4"/></g>,
    chase: <g {...p}><circle cx="12" cy="12" r="8.5"/><path d="M12 7v5l3.5 2"/></g>,
    rescue: <g {...p}><path d="M12 20s-7-4.3-7-9.2A4.3 4.3 0 0 1 12 8a4.3 4.3 0 0 1 7 2.8C19 15.7 12 20 12 20Z"/></g>,
    id: <g {...p}><rect x="3" y="5" width="18" height="14" rx="3"/><circle cx="8.5" cy="11" r="2"/><path d="M6 16c.4-1.4 1.5-2 2.5-2s2.1.6 2.5 2M14 9.5h5M14 13h5"/></g>,
    decoy: <g {...p}><circle cx="12" cy="12" r="8.5"/><path d="M12 12l4-4M9 12h.01M12 9v.01"/></g>,
    squeeze: <g {...p}><path d="M4 7h16M6 12h12M9 17h6"/></g>,
    finale: <g {...p}><path d="M5 21V4M5 4.5C8 2.5 12 6 18 4v9c-6 2-10-1.5-13 .5"/><path d="M12 9l1.2 2.4 2.6.4-1.9 1.8.5 2.6L12 15l-2.4 1.2.5-2.6-1.9-1.8 2.6-.4Z" {...F}/></g>,
    spin: <g {...p}><path d="M21 12a9 9 0 1 1-3-6.7"/><path d="M21 4v4h-4"/></g>,
    lock: <g {...p}><rect x="5" y="11" width="14" height="9" rx="2.5"/><path d="M8 11V8a4 4 0 0 1 8 0v3"/></g>,
    gift: <g {...p}><rect x="4" y="9" width="16" height="11" rx="2"/><path d="M2.5 9h19M12 9v11M12 9S10 3 7.5 4.5 9.5 9 12 9ZM12 9s2-6 4.5-4.5S14.5 9 12 9Z"/></g>,
    share: <g {...p}><circle cx="6" cy="12" r="2.5"/><circle cx="17" cy="6" r="2.5"/><circle cx="17" cy="18" r="2.5"/><path d="M8.2 10.8l6.6-3.6M8.2 13.2l6.6 3.6"/></g>,
    clock: <g {...p}><circle cx="12" cy="12" r="8.5"/><path d="M12 7v5l3.5 2"/></g>,
    route: <g {...p}><circle cx="6" cy="18" r="2.5"/><circle cx="18" cy="6" r="2.5"/><path d="M8 16c8-1 2-9 8-10"/></g>,
    medal: <g {...p}><circle cx="12" cy="14" r="6"/><path d="M9 3l3 5 3-5M12 11.5l1 2 2 .3-1.5 1.4.4 2.1-1.9-1-1.9 1 .4-2.1L9 13.8l2-.3Z"/></g>,
    plus: <g {...p}><path d="M12 5v14M5 12h14"/></g>,
    settings: <g {...p}><circle cx="12" cy="12" r="3"/><path d="M12 2v3M12 19v3M5 5l2 2M17 17l2 2M2 12h3M19 12h3M5 19l2-2M17 7l2-2"/></g>,
    bell: <g {...p}><path d="M6 9a6 6 0 0 1 12 0c0 5 2 6 2 6H4s2-1 2-6Z"/><path d="M10.5 19a2 2 0 0 0 3 0"/></g>,
    fire: <g {...p}><path d="M12 3s5 4 5 9a5 5 0 0 1-10 0c0-1.5.7-2.8 1.5-3.5C9 11 9.5 12 10 12c0-2 2-4 2-9Z"/></g>,
    arrowUp: <g {...p}><path d="M12 19V5M6 11l6-6 6 6"/></g>,
    qr: <g {...p}><rect x="4" y="4" width="6" height="6" rx="1"/><rect x="14" y="4" width="6" height="6" rx="1"/><rect x="4" y="14" width="6" height="6" rx="1"/><path d="M14 14h2v2M20 14v.01M14 20h.01M18 18h2v2"/></g>,
  };
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" style={{ display: 'block', flexShrink: 0 }}>
      {paths[name] || null}
    </svg>
  );
}

/* tappable top bar inside screens (push views) */
function TopBar({ title, onBack, right, dim }) {
  return (
    <div style={{
      display: 'flex', alignItems: 'center', gap: 12, padding: '0 16px',
      height: 52, position: 'relative', zIndex: 5,
    }}>
      {onBack && (
        <button className="tap" onClick={onBack} style={{
          width: 38, height: 38, borderRadius: 12, border: '1px solid var(--line-2)',
          background: 'rgba(255,255,255,0.04)', color: 'var(--tx)', display: 'flex',
          alignItems: 'center', justifyContent: 'center', flexShrink: 0,
        }}><Icon name="back" size={20} /></button>
      )}
      <div className="disp" style={{ fontWeight: 700, fontSize: 19, flex: 1, color: dim ? 'var(--tx-dim)' : 'var(--tx)' }}>{title}</div>
      {right}
    </div>
  );
}

/* status-bar safe top padding (clears dynamic island) */
function SafeTop({ h = 60 }) { return <div style={{ height: h, flexShrink: 0 }} />; }

/* progress bar */
function Bar({ pct, color = 'var(--fug)', glow }) {
  return (
    <div className="bar">
      <i style={{ width: Math.min(100, pct) + '%', background: color, boxShadow: glow ? `0 0 12px ${color}` : 'none' }} />
    </div>
  );
}

/* segmented progress dots for days */
function DayDots({ total, now, done }) {
  return (
    <div style={{ display: 'flex', gap: 5 }}>
      {Array.from({ length: total }).map((_, i) => {
        const d = i + 1;
        const isDone = d < now || (done && done.includes(d));
        const isNow = d === now;
        return <div key={i} style={{
          flex: 1, height: 4, borderRadius: 999,
          background: isNow ? 'var(--fug)' : isDone ? 'rgba(45,229,154,.45)' : 'rgba(255,255,255,.1)',
          boxShadow: isNow ? '0 0 8px var(--fug)' : 'none',
        }} />;
      })}
    </div>
  );
}

/* status badge for races */
function StatusBadge({ status }) {
  const map = {
    live: { t: '進行中', c: 'fug' },
    open: { t: '報名中', c: 'gold' },
    soon: { t: '即將開始', c: 'violet' },
    done: { t: '已完賽', c: 'dim' },
  };
  const s = map[status] || map.done;
  const live = status === 'live';
  return (
    <span className={'chip ' + (s.c === 'dim' ? '' : s.c)} style={{ fontWeight: 700 }}>
      {live && <span style={{ width: 6, height: 6, borderRadius: 999, background: 'var(--fug)', boxShadow: '0 0 8px var(--fug)', animation: 'dorPulse 1.4s infinite' }} />}
      {s.t}
    </span>
  );
}

const FAC = {
  fugitive: { label: '逃亡者', color: 'var(--fug)', deep: 'var(--fug-deep)', icon: 'stealth', en: 'FUGITIVE' },
  hunter:   { label: '獵人',   color: 'var(--hunt)', deep: 'var(--hunt-deep)', icon: 'target', en: 'HUNTER' },
};

Object.assign(window, { Icon, TopBar, SafeTop, Bar, DayDots, StatusBadge, FAC });
