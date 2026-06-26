/* 今日戰局 — Home / daily battle screen */
function HomeScreen({ nav, setTab }) {
  const D = window.DOR;
  const race = D.races.find(r => r.id === 'hunt2026');
  const fac = window.FAC[race.faction];
  const board = D.factionBoard;
  const today = D.missions.find(m => m.day === race.dayNow);
  const side = D.stores[0];

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop />
      {/* header row */}
      <div style={{ position: 'relative', zIndex: 2, padding: '0 18px', display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
        <div>
          <div className="eyebrow">DOR CLOUD MARATHON</div>
          <div className="disp" style={{ fontSize: 22, fontWeight: 700, marginTop: 3 }}>今晚，你逃得掉嗎？</div>
        </div>
        <div style={{ display: 'flex', gap: 8 }}>
          <button className="tap" style={iconBtn}><Icon name="bell" size={20} /></button>
        </div>
      </div>

      {/* identity card */}
      <div style={{ padding: '16px 18px 0', position: 'relative', zIndex: 2 }}>
        <div className="card tap" onClick={() => nav('progress', { id: race.id })} style={{
          padding: 0, overflow: 'hidden',
          background: `radial-gradient(120% 120% at 100% 0%, ${fac.deep} 0%, var(--bg-2) 55%)`,
          borderColor: 'rgba(45,229,154,.25)',
        }}>
          <div style={{ padding: 18 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-start' }}>
              <div>
                <div className="lbl">今日身份 ｜ {race.title}</div>
                <div className="disp" style={{ fontSize: 40, fontWeight: 700, lineHeight: 1.02, marginTop: 6, color: fac.color, textShadow: '0 0 30px rgba(45,229,154,.4)' }}>{fac.label}</div>
                <div className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)', marginTop: 4, letterSpacing: '.1em' }}>{fac.en} · {race.mine}</div>
              </div>
              <div style={{ width: 64, height: 64, borderRadius: 16, border: `1.5px solid ${fac.color}`, display: 'flex', alignItems: 'center', justifyContent: 'center', background: 'rgba(0,0,0,.25)', boxShadow: '0 0 20px rgba(45,229,154,.25)' }}>
                <Icon name={fac.icon} size={32} color={fac.color} />
              </div>
            </div>

            {/* faction battle bar */}
            <div style={{ marginTop: 18 }}>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 7, fontSize: 12 }}>
                <span className="mono tone-fug">逃亡者 {board.fugitive.val}%</span>
                <span className="lbl" style={{ letterSpacing: '.14em' }}>今晚戰局</span>
                <span className="mono tone-hunt">{board.hunter.val}% 獵人</span>
              </div>
              <div style={{ height: 9, borderRadius: 999, overflow: 'hidden', display: 'flex', background: 'var(--hunt)', boxShadow: 'inset 0 0 0 1px var(--line-2)' }}>
                <div style={{ width: board.fugitive.val + '%', background: 'var(--fug)', boxShadow: '0 0 12px var(--fug)' }} />
              </div>
              <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 6 }}>
                <span className="lbl">已逃脫 {board.fugitive.escaped}</span>
                <span className="lbl">已捕獲 {board.hunter.captured}</span>
              </div>
            </div>
          </div>
          <div style={{ borderTop: '1px solid var(--line)', padding: '11px 18px', display: 'flex', justifyContent: 'space-between', alignItems: 'center', background: 'rgba(0,0,0,.18)' }}>
            <DayChip now={race.dayNow} total={race.days} />
            <span style={{ display: 'flex', alignItems: 'center', gap: 4, color: 'var(--tx-dim)', fontSize: 13 }}>查看賽事進度 <Icon name="chev" size={15} /></span>
          </div>
        </div>
      </div>

      {/* today's main mission */}
      <SectionLabel>今日主任務</SectionLabel>
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="card tap" onClick={() => nav('mission', { day: today.day })} style={{
          padding: 16, borderColor: 'rgba(255,75,92,.28)',
          background: 'radial-gradient(130% 100% at 0% 0%, rgba(255,75,92,.10), var(--bg-2) 60%)',
        }}>
          <div style={{ display: 'flex', gap: 14 }}>
            <div style={{ width: 50, height: 50, borderRadius: 14, background: 'rgba(255,75,92,.12)', border: '1px solid rgba(255,75,92,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
              <Icon name={today.icon} size={26} color="var(--hunt)" />
            </div>
            <div style={{ flex: 1, minWidth: 0 }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                <span className="chip hunt" style={{ fontWeight: 700 }}>{today.tag}</span>
                <span className="lbl">DAY {today.day}</span>
              </div>
              <div className="disp" style={{ fontSize: 21, fontWeight: 700, marginTop: 7 }}>{today.title}</div>
              <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.5 }}>{today.desc}</div>
            </div>
          </div>

          {/* pace + distance targets */}
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <Stat label="目標配速" value={today.paceLo ? `${today.paceLo}–${today.paceHi}` : '不限'} unit="/km" tone="hunt" />
            <Stat label="基礎里程" value={today.base.toFixed(1)} unit="km" />
            <Stat label="已完成" value={today.kmDone.toFixed(1)} unit="km" tone="fug" />
          </div>

          <div style={{ marginTop: 14 }}>
            <Bar pct={today.kmDone / today.base * 100} color="var(--hunt)" glow />
            <div style={{ display: 'flex', alignItems: 'center', gap: 6, marginTop: 9, fontSize: 12.5, color: 'var(--tx-dim)' }}>
              <Icon name="rescue" size={15} color="var(--fug)" />
              完成基礎里程後，<span style={{ color: 'var(--fug)' }}>每多跑 1K 拯救 1 位夥伴</span> · 已救 {today.rescued}
            </div>
          </div>
          <button className="btn btn-hunt" style={{ marginTop: 14 }}>繼續任務 <Icon name="chev" size={18} color="#fff" /></button>
        </div>
      </div>

      {/* side mission — check in */}
      <SectionLabel>支線 ｜ 打卡任務</SectionLabel>
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="card tap" onClick={() => nav('checkin', {})} style={{ padding: 14, display: 'flex', alignItems: 'center', gap: 13 }}>
          <div style={{ width: 44, height: 44, borderRadius: 12, background: 'rgba(157,140,255,.12)', border: '1px solid rgba(157,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
            <Icon name="pin" size={22} color="var(--violet)" />
          </div>
          <div style={{ flex: 1, minWidth: 0 }}>
            <div style={{ fontWeight: 700, fontSize: 15 }}>{side.name}</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 2 }}>距你 {side.dist} km · {side.task}</div>
          </div>
          <span className="chip violet">打卡</span>
        </div>
      </div>

      {/* evening battle report */}
      <SectionLabel>今晚 20:00 ｜ 晚間戰報</SectionLabel>
      <div style={{ padding: '0 18px 18px', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 16, background: 'linear-gradient(180deg, var(--bg-2), var(--bg-1))' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <Icon name="clock" size={18} color="var(--tx-dim)" />
            <div style={{ fontSize: 13.5, color: 'var(--tx-dim)', flex: 1 }}>公布今日捕獲、救援與逆轉名單，並開啟明日任務。</div>
          </div>
          <div style={{ display: 'flex', gap: 10, marginTop: 14 }}>
            <MiniStat n="+1" l="今日救援" tone="fug" />
            <MiniStat n="12" l="連續出勤" tone="gold" />
            <MiniStat n="94" l="累積里程" />
          </div>
        </div>
      </div>
    </div>
  );
}

const iconBtn = { width: 38, height: 38, borderRadius: 12, border: '1px solid var(--line-2)', background: 'rgba(255,255,255,0.04)', color: 'var(--tx)', display: 'flex', alignItems: 'center', justifyContent: 'center' };

function SectionLabel({ children }) {
  return <div className="lbl" style={{ padding: '22px 18px 11px', position: 'relative', zIndex: 2 }}>{children}</div>;
}

function DayChip({ now, total }) {
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 9 }}>
      <span className="mono" style={{ fontSize: 12, color: 'var(--fug)' }}>DAY {now}/{total}</span>
      <div style={{ width: 84 }}><DayDots total={total} now={now} /></div>
    </div>
  );
}

function Stat({ label, value, unit, tone }) {
  const c = tone === 'hunt' ? 'var(--hunt)' : tone === 'fug' ? 'var(--fug)' : 'var(--tx)';
  return (
    <div style={{ flex: 1, background: 'rgba(255,255,255,.03)', border: '1px solid var(--line)', borderRadius: 12, padding: '9px 11px' }}>
      <div className="lbl" style={{ fontSize: 9 }}>{label}</div>
      <div style={{ marginTop: 3, display: 'flex', alignItems: 'baseline', gap: 3 }}>
        <span className="num" style={{ fontSize: 18, color: c }}>{value}</span>
        <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>{unit}</span>
      </div>
    </div>
  );
}

function MiniStat({ n, l, tone }) {
  const c = tone === 'fug' ? 'var(--fug)' : tone === 'gold' ? 'var(--gold)' : 'var(--tx)';
  return (
    <div style={{ flex: 1, textAlign: 'center', padding: '8px 4px', background: 'rgba(255,255,255,.03)', borderRadius: 12, border: '1px solid var(--line)' }}>
      <div className="num" style={{ fontSize: 20, color: c }}>{n}</div>
      <div className="lbl" style={{ fontSize: 9, marginTop: 2 }}>{l}</div>
    </div>
  );
}

Object.assign(window, { HomeScreen, SectionLabel, Stat, MiniStat, iconBtn });
