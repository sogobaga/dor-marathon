/* 個人資料 · 完賽紀錄 */

function ProfileScreen({ nav, setTab }) {
  const D = window.DOR;
  const p = D.profile;
  const fac = window.FAC[p.faction];
  const earned = Math.floor(p.totalKm / 50) - p.spinsUsed;

  const menu = [
    { icon: 'medal', t: '完賽紀錄', d: `${p.races} 場完賽`, go: () => nav('records', {}) },
    { icon: 'route', t: '累積里程', d: `${p.totalKm} km`, go: () => setTab('wheel') },
    { icon: 'id', t: '九宮格集點卡', d: '5 / 9 已收集', go: () => nav('stickers', {}) },
    { icon: 'wheel', t: '里程轉盤', d: earned > 0 ? `${earned} 次可轉` : '尚無次數', go: () => setTab('wheel') },
  ];

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop />
      <div style={{ display: 'flex', justifyContent: 'flex-end', padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <button className="tap" style={iconBtn}><Icon name="settings" size={19} /></button>
      </div>

      {/* identity card */}
      <div style={{ padding: '6px 18px 0', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 18, background: `radial-gradient(120% 120% at 100% 0, ${fac.deep}, var(--bg-2) 58%)`, borderColor: 'rgba(45,229,154,.25)' }}>
          <div style={{ display: 'flex', gap: 15, alignItems: 'center' }}>
            <div className="ph" style={{ width: 72, height: 72, borderRadius: 20, flexShrink: 0, border: `1.5px solid ${fac.color}` }}>{p.avatar}</div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div className="disp" style={{ fontSize: 23, fontWeight: 700 }}>{p.name}</div>
              <div className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>{p.handle}</div>
              <div style={{ display: 'flex', gap: 7, marginTop: 9 }}>
                <span className="chip fug" style={{ fontWeight: 700 }}><Icon name={fac.icon} size={12} color="var(--fug)" /> {fac.label}</span>
                <span className="chip">{p.team}</span>
              </div>
            </div>
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 16 }}>
            <ProfStat n={p.races} l="完賽場次" />
            <ProfStat n={p.totalKm} l="累積里程" tone="fug" />
            <ProfStat n={p.rescues} l="救援人數" tone="gold" />
            <ProfStat n={p.streak} l="連續出勤" tone="hunt" />
          </div>
        </div>
      </div>

      {/* menu */}
      <SectionLabel>我的</SectionLabel>
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
          {menu.map((m, i) => (
            <div key={i} className="tap" onClick={m.go} style={{ display: 'flex', alignItems: 'center', gap: 13, padding: '14px 15px', borderTop: i ? '1px solid var(--line)' : 'none' }}>
              <div style={{ width: 38, height: 38, borderRadius: 11, background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
                <Icon name={m.icon} size={19} color="var(--tx-dim)" />
              </div>
              <div style={{ flex: 1 }}>
                <div style={{ fontWeight: 600, fontSize: 15 }}>{m.t}</div>
                <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>{m.d}</div>
              </div>
              <Icon name="chev" size={16} color="var(--tx-faint)" />
            </div>
          ))}
        </div>
      </div>

      {/* identity cards owned */}
      <SectionLabel>身份卡收藏</SectionLabel>
      <div style={{ padding: '0 18px 24px', display: 'flex', gap: 10, overflowX: 'auto', position: 'relative', zIndex: 2 }}>
        {[['逃亡者', 'fug', 'stealth'], ['獵人', 'hunt', 'target'], ['誘餌', 'violet', 'decoy'], ['訊號', 'gold', 'route']].map(([t, c, ic], i) => (
          <div key={i} style={{ width: 96, flexShrink: 0, borderRadius: 14, padding: 14, textAlign: 'center', border: `1px solid var(--${c})55`, background: `radial-gradient(120% 120% at 50% 0, var(--${c})22, var(--bg-2))` }}>
            <Icon name={ic} size={26} color={`var(--${c})`} style={{ margin: '0 auto' }} />
            <div style={{ fontWeight: 700, fontSize: 13, marginTop: 8 }}>{t}</div>
            <div className="lbl" style={{ fontSize: 8, marginTop: 3 }}>IDENTITY</div>
          </div>
        ))}
      </div>
    </div>
  );
}

function ProfStat({ n, l, tone }) {
  const c = tone ? `var(--${tone})` : 'var(--tx)';
  return (
    <div style={{ flex: 1, textAlign: 'center' }}>
      <div className="num" style={{ fontSize: 22, color: c }}>{n}</div>
      <div className="lbl" style={{ fontSize: 8.5, marginTop: 3 }}>{l}</div>
    </div>
  );
}

/* ---------- 完賽紀錄 ---------- */
function RecordsScreen({ back }) {
  const D = window.DOR;
  const recs = D.records;
  const totalKm = recs.reduce((a, r) => a + r.dist, 0);

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop h={50} />
      <TopBar title="完賽紀錄" onBack={back} />

      {/* summary */}
      <div style={{ padding: '4px 18px 0', position: 'relative', zIndex: 2 }}>
        <div className="card card-pad" style={{ display: 'flex', justifyContent: 'space-around' }}>
          <div style={{ textAlign: 'center' }}>
            <div className="num" style={{ fontSize: 30 }}>{recs.length}</div>
            <div className="lbl" style={{ marginTop: 3 }}>完賽場次</div>
          </div>
          <div style={{ width: 1, background: 'var(--line)' }} />
          <div style={{ textAlign: 'center' }}>
            <div className="num tone-fug" style={{ fontSize: 30 }}>{totalKm}</div>
            <div className="lbl" style={{ marginTop: 3 }}>完賽里程 km</div>
          </div>
          <div style={{ width: 1, background: 'var(--line)' }} />
          <div style={{ textAlign: 'center' }}>
            <div className="num tone-gold" style={{ fontSize: 30 }}>4</div>
            <div className="lbl" style={{ marginTop: 3 }}>獲得獎牌</div>
          </div>
        </div>
      </div>

      <SectionLabel>歷史賽事</SectionLabel>
      <div style={{ padding: '0 18px 24px', display: 'flex', flexDirection: 'column', gap: 12, position: 'relative', zIndex: 2 }}>
        {recs.map(r => {
          const fac = window.FAC[r.faction];
          return (
            <div key={r.id} className="card" style={{ padding: 15, display: 'flex', gap: 14, alignItems: 'center' }}>
              {/* medal */}
              <div style={{ width: 54, height: 54, borderRadius: '50%', border: `2px solid ${r.medal}`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0, background: `${r.medal}1a`, boxShadow: `0 0 16px ${r.medal}40` }}>
                <Icon name="medal" size={26} color={r.medal} />
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontWeight: 700, fontSize: 15.5 }}>{r.title}</span>
                  <span className="chip" style={{ fontSize: 9, padding: '1px 7px', color: fac.color, borderColor: fac.color + '55' }}>{r.rank}</span>
                </div>
                <div style={{ display: 'flex', gap: 14, marginTop: 8 }}>
                  <Field l="距離" v={`${r.dist}K`} />
                  <Field l="完賽時間" v={r.time} />
                  <Field l="日期" v={r.date} />
                </div>
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

function Field({ l, v }) {
  return (
    <div>
      <div className="lbl" style={{ fontSize: 8.5 }}>{l}</div>
      <div className="mono" style={{ fontSize: 12.5, marginTop: 2 }}>{v}</div>
    </div>
  );
}

Object.assign(window, { ProfileScreen, RecordsScreen, ProfStat, Field });
