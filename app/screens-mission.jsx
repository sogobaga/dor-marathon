/* 賽事每日任務目標 + 打卡任務(門市) */

function MissionScreen({ route, back }) {
  const D = window.DOR;
  const m = D.missions.find(x => x.day === route.day) || D.missions[2];
  const [km, setKm] = React.useState(m.kmDone);
  const baseReached = km >= m.base;
  const extra = Math.max(0, Math.floor(km - m.base));
  const rescued = Math.min(5, extra + m.rescued);
  const report = () => setKm(k => Math.round((k + 1) * 10) / 10);

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop h={50} />
      <TopBar title={`DAY ${m.day} 任務`} onBack={back} />

      <div style={{ padding: '4px 18px 20px', position: 'relative', zIndex: 2 }}>
        {/* mission header */}
        <div className="card" style={{ padding: 18, background: 'radial-gradient(130% 100% at 0 0, rgba(255,75,92,.12), var(--bg-2) 55%)', borderColor: 'rgba(255,75,92,.28)' }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
            <span className="chip hunt" style={{ fontWeight: 700 }}>{m.tag}</span>
            <span className="lbl">{m.type === 'pace' ? '配速任務' : m.type === 'rescue' ? '救援任務' : '里程任務'}</span>
          </div>
          <div className="disp" style={{ fontSize: 30, fontWeight: 700, marginTop: 10 }}>{m.title}</div>
          <p style={{ fontSize: 14, color: 'var(--tx-dim)', lineHeight: 1.65, margin: '8px 0 0' }}>{m.desc}</p>
        </div>

        {/* targets */}
        <div style={{ display: 'flex', gap: 12, marginTop: 14 }}>
          <BigTarget label="目標配速" value={m.paceLo ? `${m.paceLo}` : '不限'} sub={m.paceLo ? `– ${m.paceHi} /km` : '自由配速'} tone="hunt" icon="chase" />
          <BigTarget label="基礎里程" value={m.base.toFixed(1)} sub="km 完成目標" tone="fug" icon="route" />
        </div>

        {/* pace gauge */}
        {m.paceLo && <PaceGauge lo={m.paceLo} hi={m.paceHi} />}

        {/* live progress */}
        <div className="lbl" style={{ marginTop: 22, marginBottom: 10 }}>今日進度</div>
        <div className="card card-pad">
          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'space-between' }}>
            <div style={{ display: 'flex', alignItems: 'baseline', gap: 5 }}>
              <span className="num" style={{ fontSize: 36, color: baseReached ? 'var(--fug)' : 'var(--hunt)' }}>{km.toFixed(1)}</span>
              <span className="mono" style={{ color: 'var(--tx-dim)' }}>/ {m.base.toFixed(1)} km</span>
            </div>
            {baseReached ? <span className="chip fug"><Icon name="check" size={13} color="var(--fug)" /> 基礎達成</span>
              : <span className="chip hunt">尚差 {(m.base - km).toFixed(1)}K</span>}
          </div>
          <div style={{ marginTop: 12 }}><Bar pct={km / m.base * 100} color={baseReached ? 'var(--fug)' : 'var(--hunt)'} glow /></div>
        </div>

        {/* rescue ladder */}
        <div className="lbl" style={{ marginTop: 22, marginBottom: 10 }}>救援階梯 ｜ 多跑 1K = 救 1 人</div>
        <div className="card card-pad" style={{ borderColor: 'rgba(45,229,154,.22)' }}>
          <div style={{ display: 'flex', gap: 9, justifyContent: 'space-between' }}>
            {Array.from({ length: 5 }).map((_, i) => {
              const on = i < rescued;
              return (
                <div key={i} style={{ flex: 1, textAlign: 'center' }}>
                  <div style={{ height: 50, borderRadius: 12, border: `1px solid ${on ? 'rgba(45,229,154,.4)' : 'var(--line-2)'}`, background: on ? 'rgba(45,229,154,.12)' : 'rgba(255,255,255,.02)', display: 'flex', alignItems: 'center', justifyContent: 'center', boxShadow: on ? 'var(--glow-fug)' : 'none', transition: 'all .2s' }}>
                    <Icon name="rescue" size={22} color={on ? 'var(--fug)' : 'var(--tx-faint)'} />
                  </div>
                  <div className="mono" style={{ fontSize: 9, marginTop: 5, color: 'var(--tx-faint)' }}>+{m.base + i + 1}K</div>
                </div>
              );
            })}
          </div>
          <div style={{ display: 'flex', alignItems: 'center', gap: 7, marginTop: 13, fontSize: 13 }}>
            <Icon name="rescue" size={16} color="var(--fug)" />
            <span style={{ color: 'var(--tx-dim)' }}>已拯救 <span className="num tone-fug" style={{ fontSize: 15 }}>{rescued}</span> 位夥伴{m.type === 'rescue' && <span className="tone-fug"> · 今日救援值加倍</span>}</span>
          </div>
        </div>

        <button className="btn btn-primary" style={{ marginTop: 18 }} onClick={report}>
          <Icon name="plus" size={18} color="#05140e" /> 回報跑步 +1.0K
        </button>
        <div style={{ textAlign: 'center', marginTop: 10 }}>
          <span className="lbl">由 Garmin · Apple Health · Strava 自動同步</span>
        </div>
      </div>
    </div>
  );
}

function BigTarget({ label, value, sub, tone, icon }) {
  const c = `var(--${tone})`;
  return (
    <div className="card" style={{ flex: 1, padding: 14 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
        <span className="lbl">{label}</span>
        <Icon name={icon} size={16} color={c} />
      </div>
      <div className="num" style={{ fontSize: 28, color: c, marginTop: 8 }}>{value}</div>
      <div className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)', marginTop: 2 }}>{sub}</div>
    </div>
  );
}

function PaceGauge({ lo, hi }) {
  return (
    <div className="card card-pad" style={{ marginTop: 14 }}>
      <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'center', marginBottom: 12 }}>
        <span className="lbl">配速區間 ｜ 維持在綠區</span>
        <span className="mono" style={{ fontSize: 11, color: 'var(--fug)' }}>目前 5:02</span>
      </div>
      <div style={{ position: 'relative', height: 12 }}>
        <div style={{ position: 'absolute', inset: 0, borderRadius: 999, background: 'linear-gradient(90deg, var(--hunt) 0%, var(--hunt) 28%, var(--fug) 38%, var(--fug) 62%, var(--gold) 72%, var(--gold) 100%)', opacity: .35 }} />
        <div style={{ position: 'absolute', left: '38%', width: '24%', top: -3, bottom: -3, borderRadius: 999, border: '1.5px solid var(--fug)', boxShadow: 'var(--glow-fug)' }} />
        <div style={{ position: 'absolute', left: '50%', top: -6, width: 3, height: 24, borderRadius: 9, background: '#fff', transform: 'translateX(-50%)' }} />
      </div>
      <div style={{ display: 'flex', justifyContent: 'space-between', marginTop: 9 }}>
        <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>快 {hi}</span>
        <span className="mono" style={{ fontSize: 10, color: 'var(--fug)' }}>{lo}–{hi} 安全區</span>
        <span className="mono" style={{ fontSize: 10, color: 'var(--tx-faint)' }}>慢 {lo}</span>
      </div>
    </div>
  );
}

/* ---------- 打卡任務 + 門市 ---------- */
function CheckinScreen({ back }) {
  const D = window.DOR;
  const [stores, setStores] = React.useState(D.stores);
  const [open, setOpen] = React.useState(null);
  const done = stores.filter(s => s.stamp).length;
  const check = (id) => setStores(ss => ss.map(s => s.id === id ? { ...s, stamp: true } : s));

  return (
    <div className="dor-scroll">
      <SafeTop h={50} />
      <TopBar title="打卡任務" onBack={back} />

      {/* map */}
      <div style={{ padding: '4px 18px 0' }}>
        <div className="ph" style={{ height: 168, borderRadius: 18, position: 'relative', overflow: 'hidden' }}>
          地圖 ｜ 門市打卡點分佈
          {[[28, 40], [62, 30], [46, 64], [78, 70]].map(([l, t], i) => (
            <div key={i} style={{ position: 'absolute', left: l + '%', top: t + '%', transform: 'translate(-50%,-100%)' }}>
              <Icon name="pin" size={26} color={stores[i] && stores[i].stamp ? 'var(--fug)' : 'var(--violet)'} />
            </div>
          ))}
        </div>
      </div>

      {/* task summary */}
      <div style={{ padding: '14px 18px 0' }}>
        <div className="card card-pad" style={{ display: 'flex', alignItems: 'center', gap: 13, borderColor: 'rgba(157,140,255,.25)' }}>
          <div style={{ width: 46, height: 46, borderRadius: 12, background: 'rgba(157,140,255,.12)', border: '1px solid rgba(157,140,255,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Icon name="qr" size={24} color="var(--violet)" />
          </div>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 700 }}>到店出示身份卡打卡</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 2 }}>本賽事已打卡 {done}/{stores.length} 間門市</div>
          </div>
          <div className="num tone-violet" style={{ fontSize: 22 }}>{done}/{stores.length}</div>
        </div>
      </div>

      <SectionLabel>補給門市</SectionLabel>
      <div style={{ padding: '0 18px 20px', display: 'flex', flexDirection: 'column', gap: 12 }}>
        {stores.map(s => (
          <div key={s.id} className="card" style={{ padding: 0, overflow: 'hidden', borderColor: s.stamp ? 'rgba(45,229,154,.3)' : 'var(--line)' }}>
            <div className="tap" onClick={() => setOpen(open === s.id ? null : s.id)} style={{ padding: 14, display: 'flex', gap: 12, alignItems: 'flex-start' }}>
              <div style={{ width: 42, height: 42, borderRadius: 11, background: s.stamp ? 'rgba(45,229,154,.12)' : 'rgba(255,255,255,.04)', border: '1px solid ' + (s.stamp ? 'rgba(45,229,154,.35)' : 'var(--line-2)'), display: 'flex', alignItems: 'center', justifyContent: 'center', flexShrink: 0 }}>
                <Icon name={s.stamp ? 'check' : 'pin'} size={20} color={s.stamp ? 'var(--fug)' : 'var(--violet)'} />
              </div>
              <div style={{ flex: 1, minWidth: 0 }}>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <span style={{ fontWeight: 700, fontSize: 14.5 }}>{s.name}</span>
                  {s.stamp && <span className="chip fug" style={{ fontSize: 9, padding: '1px 7px' }}>已打卡</span>}
                </div>
                <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 3 }}>{s.addr}</div>
                <div style={{ display: 'flex', gap: 12, marginTop: 7 }}>
                  <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}><Icon name="pin" size={11} color="var(--tx-faint)" style={{ display: 'inline', verticalAlign: '-1px' }} /> {s.dist} km</span>
                  <span className="mono" style={{ fontSize: 11, color: 'var(--tx-faint)' }}>{s.hours}</span>
                </div>
              </div>
              <Icon name={open === s.id ? 'chevDown' : 'chev'} size={16} color="var(--tx-faint)" />
            </div>
            {open === s.id && (
              <div style={{ padding: '0 14px 14px', borderTop: '1px solid var(--line)', marginTop: 0 }}>
                <div style={{ display: 'flex', gap: 10, padding: '12px 0' }}>
                  <div className="ph" style={{ width: 60, height: 60, borderRadius: 10, flexShrink: 0 }}>QR</div>
                  <div style={{ fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.5, flex: 1 }}>{s.task}</div>
                </div>
                {!s.stamp
                  ? <button className="btn" style={{ background: 'var(--violet)', color: '#150f2e' }} onClick={() => check(s.id)}><Icon name="qr" size={18} color="#150f2e" /> 掃碼打卡</button>
                  : <div style={{ textAlign: 'center', padding: 10, color: 'var(--fug)', fontWeight: 700 }}>✓ 打卡完成 · +50 救援值</div>}
              </div>
            )}
          </div>
        ))}
      </div>
    </div>
  );
}

Object.assign(window, { MissionScreen, CheckinScreen, BigTarget, PaceGauge });
