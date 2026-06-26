/* 賽事列表 · 賽事詳情+報名 · 賽事進度 */

function RaceListScreen({ nav }) {
  const D = window.DOR;
  const [filter, setFilter] = React.useState('all');
  const tabs = [['all', '全部'], ['open', '報名中'], ['live', '進行中'], ['done', '已完賽']];
  const list = D.races.filter(r => filter === 'all' || r.status === filter);
  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop />
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="eyebrow">EVENTS</div>
        <div className="disp" style={{ fontSize: 26, fontWeight: 700, marginTop: 3 }}>賽事</div>
      </div>
      {/* filter */}
      <div style={{ display: 'flex', gap: 8, padding: '14px 18px 4px', overflowX: 'auto', position: 'relative', zIndex: 2 }}>
        {tabs.map(([k, t]) => (
          <button key={k} className="tap" onClick={() => setFilter(k)} style={{
            border: '1px solid ' + (filter === k ? 'var(--fug)' : 'var(--line-2)'),
            background: filter === k ? 'rgba(45,229,154,.12)' : 'rgba(255,255,255,.03)',
            color: filter === k ? 'var(--fug)' : 'var(--tx-dim)',
            borderRadius: 999, padding: '7px 15px', fontSize: 13, fontWeight: 600, whiteSpace: 'nowrap',
            fontFamily: 'var(--font-disp)',
          }}>{t}</button>
        ))}
      </div>
      <div style={{ padding: '12px 18px 18px', display: 'flex', flexDirection: 'column', gap: 14, position: 'relative', zIndex: 2 }}>
        {list.map(r => <RaceCard key={r.id} r={r} onClick={() => nav('raceDetail', { id: r.id })} />)}
      </div>
    </div>
  );
}

function RaceCard({ r, onClick }) {
  const fac = r.faction ? window.FAC[r.faction] : null;
  return (
    <div className="card tap" onClick={onClick} style={{ padding: 0, overflow: 'hidden' }}>
      <div className="ph" style={{ height: 122, borderRadius: 0, position: 'relative' }}>
        {r.hero}
        <div style={{ position: 'absolute', inset: 0, background: 'linear-gradient(180deg, transparent 30%, rgba(13,15,20,.92))' }} />
        <div style={{ position: 'absolute', top: 12, left: 12 }}><StatusBadge status={r.status} /></div>
        <div style={{ position: 'absolute', left: 14, bottom: 11, right: 14 }}>
          <div className="mono" style={{ fontSize: 10, letterSpacing: '.2em', color: 'var(--tx-dim)' }}>{r.sub}</div>
          <div className="disp" style={{ fontSize: 24, fontWeight: 700, lineHeight: 1.05 }}>{r.title}</div>
        </div>
      </div>
      <div style={{ padding: 14 }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8, flexWrap: 'wrap' }}>
          <span className="chip">{r.world}</span>
          {r.distances.map(d => <span key={d} className="chip" style={{ fontWeight: 700 }}>{d}K</span>)}
        </div>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginTop: 12 }}>
          <div style={{ display: 'flex', gap: 16 }}>
            <KV label="賽期" value={`${r.start}–${r.end}`} />
            <KV label="參賽隊伍" value={r.teams ? r.teams.toLocaleString() : '—'} />
          </div>
          <Icon name="chev" size={18} color="var(--tx-faint)" />
        </div>
      </div>
    </div>
  );
}

function KV({ label, value }) {
  return (
    <div>
      <div className="lbl" style={{ fontSize: 9 }}>{label}</div>
      <div className="mono" style={{ fontSize: 13, marginTop: 2, color: 'var(--tx)' }}>{value}</div>
    </div>
  );
}

/* ---------- detail + registration ---------- */
function RaceDetailScreen({ route, nav, back }) {
  const D = window.DOR;
  const r = D.races.find(x => x.id === route.id);
  const [pick, setPick] = React.useState(r.distances[r.distances.length - 1]);
  const [step, setStep] = React.useState(r.status === 'live' || r.status === 'done' ? 'joined' : 'view'); // view | confirm | joined
  const fac = r.faction ? window.FAC[r.faction] : null;

  return (
    <div className="dor-scroll">
      {/* hero */}
      <div style={{ position: 'relative' }}>
        <div className="ph" style={{ height: 270, borderRadius: 0 }}>{r.hero}</div>
        <div style={{ position: 'absolute', inset: 0, background: 'linear-gradient(180deg, rgba(7,8,11,.5) 0%, transparent 30%, var(--bg-1) 96%)' }} />
        <div style={{ position: 'absolute', top: 0, left: 0, right: 0 }}><SafeTop h={50} />
          <div style={{ padding: '0 16px' }}>
            <button className="tap" onClick={back} style={iconBtn}><Icon name="back" size={20} /></button>
          </div>
        </div>
        <div style={{ position: 'absolute', left: 18, right: 18, bottom: 14 }}>
          <StatusBadge status={r.status} />
          <div className="mono" style={{ fontSize: 11, letterSpacing: '.2em', color: 'var(--tx-dim)', marginTop: 10 }}>{r.sub}</div>
          <div className="disp" style={{ fontSize: 34, fontWeight: 700, lineHeight: 1.02 }}>{r.title}</div>
        </div>
      </div>

      <div style={{ padding: '4px 18px 20px' }}>
        <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', marginBottom: 14 }}>
          <span className="chip">{r.world}</span>
          <span className="chip">{r.start}–{r.end}</span>
          <span className="chip">{r.days} 日賽程</span>
        </div>
        <p style={{ fontSize: 14.5, lineHeight: 1.7, color: 'var(--tx-dim)', margin: '0 0 4px' }}>{r.blurb}</p>

        {/* world rules */}
        <div className="lbl" style={{ marginTop: 22, marginBottom: 11 }}>玩法引擎</div>
        <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
          <RuleRow icon="id" tone="fug" t="身份分配" d="開賽揭曉獵人或逃亡者，每日收到陣營任務。" />
          <RuleRow icon="chase" tone="hunt" t="每日任務卡" d="距離、配速、路線、救援 — 在條件下完成遊戲行動。" />
          <RuleRow icon="rescue" tone="violet" t="小隊救援" d="完成基礎里程後，每多 1K 釋放一位被捕夥伴。" />
        </div>

        {/* registration / progress */}
        {step === 'joined' ? (
          <JoinedBlock r={r} fac={fac} nav={nav} />
        ) : (
          <>
            <div className="lbl" style={{ marginTop: 24, marginBottom: 11 }}>選擇報名組別</div>
            <div style={{ display: 'flex', gap: 10 }}>
              {r.distances.map(d => (
                <button key={d} className="tap" onClick={() => setPick(d)} style={{
                  flex: 1, borderRadius: 16, padding: '16px 0', textAlign: 'center',
                  border: '1.5px solid ' + (pick === d ? 'var(--fug)' : 'var(--line-2)'),
                  background: pick === d ? 'rgba(45,229,154,.1)' : 'rgba(255,255,255,.03)',
                  boxShadow: pick === d ? 'var(--glow-fug)' : 'none',
                }}>
                  <div className="num" style={{ fontSize: 28, color: pick === d ? 'var(--fug)' : 'var(--tx)' }}>{d}</div>
                  <div className="lbl" style={{ marginTop: 2 }}>公里</div>
                </button>
              ))}
            </div>
            <div className="card" style={{ padding: 14, marginTop: 14, display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <div>
                <div className="lbl">累積里程目標</div>
                <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 3 }}>完賽後將累積 <span className="num tone-fug" style={{ fontSize: 15 }}>{pick}</span> km 至轉盤</div>
              </div>
              <Icon name="wheel" size={26} color="var(--fug)" />
            </div>
          </>
        )}
      </div>

      {/* sticky CTA */}
      {step !== 'joined' && (
        <div style={{ position: 'sticky', bottom: 0, padding: '14px 18px calc(14px + env(safe-area-inset-bottom))', background: 'linear-gradient(180deg, transparent, var(--bg-1) 26%)' }}>
          {step === 'view' && <button className="btn btn-primary" onClick={() => setStep('confirm')}>立即報名 · {pick}K</button>}
          {step === 'confirm' && (
            <div className="card" style={{ padding: 16 }}>
              <div className="disp" style={{ fontWeight: 700, fontSize: 17 }}>確認報名</div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 12, fontSize: 14 }}>
                <span style={{ color: 'var(--tx-dim)' }}>{r.title}</span><span className="mono">{pick}K 組</span>
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 8, fontSize: 14 }}>
                <span style={{ color: 'var(--tx-dim)' }}>報名費</span><span className="mono tone-gold">NT$ 690</span>
              </div>
              <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
                <button className="btn btn-ghost" style={{ flex: 1 }} onClick={() => setStep('view')}>返回</button>
                <button className="btn btn-primary" style={{ flex: 1.4 }} onClick={() => setStep('joined')}>確認付款</button>
              </div>
            </div>
          )}
        </div>
      )}
    </div>
  );
}

function RuleRow({ icon, tone, t, d }) {
  const c = `var(--${tone})`;
  return (
    <div style={{ display: 'flex', gap: 12, alignItems: 'flex-start' }}>
      <div style={{ width: 38, height: 38, borderRadius: 11, background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
        <Icon name={icon} size={20} color={c} />
      </div>
      <div style={{ flex: 1 }}>
        <div style={{ fontWeight: 700, fontSize: 14.5 }}>{t}</div>
        <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 2, lineHeight: 1.5 }}>{d}</div>
      </div>
    </div>
  );
}

function JoinedBlock({ r, fac, nav }) {
  const done = r.status === 'done';
  return (
    <div className="card" style={{ padding: 16, marginTop: 22, borderColor: done ? 'var(--line-2)' : 'rgba(45,229,154,.3)' }}>
      <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Icon name="check" size={18} color={done ? 'var(--tx-dim)' : 'var(--fug)'} />
          <span style={{ fontWeight: 700 }}>{done ? '已完賽' : '已報名 · ' + r.myDistance + 'K 組'}</span>
        </div>
        {fac && <span className={'chip ' + fac.label}><span style={{ color: fac.color }}>{fac.label}</span></span>}
      </div>
      {!done && (
        <>
          <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 14, marginBottom: 7 }}>
            <span>賽事進度</span><span className="mono">{r.progressKm} / {r.goalKm} km</span>
          </div>
          <Bar pct={r.progressKm / r.goalKm * 100} glow />
          <button className="btn btn-primary" style={{ marginTop: 16 }} onClick={() => nav('progress', { id: r.id })}>查看賽事進度</button>
        </>
      )}
      {done && (
        <button className="btn btn-ghost" style={{ marginTop: 14 }} onClick={() => nav('records', {})}>查看完賽紀錄</button>
      )}
    </div>
  );
}

Object.assign(window, { RaceListScreen, RaceDetailScreen, RaceCard, KV });
