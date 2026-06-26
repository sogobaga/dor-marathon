/* 賽事進度狀況 */
function ProgressScreen({ route, nav, back }) {
  const D = window.DOR;
  const r = D.races.find(x => x.id === route.id) || D.races[0];
  const board = D.factionBoard;
  const pct = r.progressKm / r.goalKm;
  const R = 78, C = 2 * Math.PI * R;

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop h={50} />
      <TopBar title="賽事進度" onBack={back} right={<button className="tap" style={iconBtn}><Icon name="share" size={18} /></button>} />

      {/* ring */}
      <div style={{ display: 'flex', flexDirection: 'column', alignItems: 'center', padding: '14px 0 8px', position: 'relative', zIndex: 2 }}>
        <div style={{ position: 'relative', width: 200, height: 200 }}>
          <svg width="200" height="200" style={{ transform: 'rotate(-90deg)' }}>
            <circle cx="100" cy="100" r={R} fill="none" stroke="rgba(255,255,255,.08)" strokeWidth="12" />
            <circle cx="100" cy="100" r={R} fill="none" stroke="var(--fug)" strokeWidth="12" strokeLinecap="round"
              strokeDasharray={C} strokeDashoffset={C * (1 - pct)} style={{ filter: 'drop-shadow(0 0 8px var(--fug))' }} />
          </svg>
          <div style={{ position: 'absolute', inset: 0, display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center' }}>
            <div className="lbl">已完成里程</div>
            <div className="num" style={{ fontSize: 46, lineHeight: 1, marginTop: 4, color: 'var(--fug)' }}>{r.progressKm}</div>
            <div className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 2 }}>/ {r.goalKm} km · {r.myDistance}K 組</div>
          </div>
        </div>
        <div style={{ display: 'flex', gap: 10, marginTop: 6 }}>
          <span className="chip fug"><Icon name="stealth" size={13} color="var(--fug)" /> 逃亡者</span>
          <span className="chip">{r.mine}</span>
        </div>
      </div>

      {/* faction standings */}
      <SectionLabel>今晚戰局</SectionLabel>
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="card card-pad">
          <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 8 }}>
            <span className="mono tone-fug" style={{ fontSize: 13 }}>逃亡者 {board.fugitive.val}%</span>
            <span className="mono tone-hunt" style={{ fontSize: 13 }}>{board.hunter.val}% 獵人</span>
          </div>
          <div style={{ height: 10, borderRadius: 999, overflow: 'hidden', display: 'flex', background: 'var(--hunt)' }}>
            <div style={{ width: board.fugitive.val + '%', background: 'var(--fug)', boxShadow: '0 0 12px var(--fug)' }} />
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <MiniStat n={board.fugitive.escaped} l="已逃脫" tone="fug" />
            <MiniStat n={board.hunter.captured} l="已捕獲" tone="hunt" />
            <MiniStat n={D.profile.rescues} l="我的救援" tone="gold" />
          </div>
        </div>
      </div>

      {/* team ranking — guild war */}
      <SectionLabel>公會戰 ｜ 跑團排行</SectionLabel>
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
          {[
            { rank: 1, name: '南港獵殺者', rate: 94, me: false },
            { rank: 2, name: '台北夜行者', rate: 88, me: true },
            { rank: 3, name: '內湖逃逸線', rate: 81, me: false },
          ].map((t, i) => (
            <div key={i} style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '13px 15px', borderTop: i ? '1px solid var(--line)' : 'none', background: t.me ? 'rgba(45,229,154,.06)' : 'transparent' }}>
              <span className="num" style={{ width: 22, fontSize: 17, color: t.rank === 1 ? 'var(--gold)' : 'var(--tx-faint)' }}>{t.rank}</span>
              <span style={{ flex: 1, fontWeight: t.me ? 700 : 500, fontSize: 14.5 }}>{t.name}{t.me && <span className="chip fug" style={{ marginLeft: 8, fontSize: 9, padding: '2px 7px' }}>我的</span>}</span>
              <div style={{ width: 70 }}><Bar pct={t.rate} glow={t.me} /></div>
              <span className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)', width: 34, textAlign: 'right' }}>{t.rate}%</span>
            </div>
          ))}
        </div>
      </div>

      {/* day timeline */}
      <SectionLabel>每日任務進度</SectionLabel>
      <div style={{ padding: '0 18px 20px', position: 'relative', zIndex: 2, display: 'flex', flexDirection: 'column', gap: 8 }}>
        {D.missions.map(m => {
          const state = m.day < r.dayNow ? 'done' : m.day === r.dayNow ? 'now' : 'lock';
          const c = state === 'done' ? 'var(--fug)' : state === 'now' ? 'var(--hunt)' : 'var(--tx-faint)';
          return (
            <div key={m.day} className={'card tap'} onClick={() => state !== 'lock' && nav('mission', { day: m.day })} style={{
              padding: '12px 14px', display: 'flex', alignItems: 'center', gap: 13,
              opacity: state === 'lock' ? .55 : 1,
              borderColor: state === 'now' ? 'rgba(255,75,92,.3)' : 'var(--line)',
            }}>
              <div style={{ width: 40, height: 40, borderRadius: 11, border: `1px solid ${c}33`, background: `${c}14`, display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                {state === 'done' ? <Icon name="check" size={20} color={c} /> : state === 'lock' ? <Icon name="lock" size={17} color={c} /> : <Icon name={m.icon} size={20} color={c} />}
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 7 }}>
                  <span className="lbl" style={{ fontSize: 9 }}>DAY {m.day}</span>
                  {state === 'now' && <span className="chip hunt" style={{ fontSize: 9, padding: '1px 7px' }}>進行中</span>}
                </div>
                <div style={{ fontWeight: 700, fontSize: 14.5, marginTop: 2 }}>{m.title}</div>
              </div>
              <div style={{ textAlign: 'right' }}>
                <div className="mono" style={{ fontSize: 12, color: state === 'done' ? 'var(--fug)' : 'var(--tx-dim)' }}>{m.kmDone.toFixed(1)}/{m.base.toFixed(1)}K</div>
                {state !== 'lock' && <Icon name="chev" size={14} color="var(--tx-faint)" style={{ marginLeft: 'auto' }} />}
              </div>
            </div>
          );
        })}
      </div>
    </div>
  );
}

Object.assign(window, { ProgressScreen });
