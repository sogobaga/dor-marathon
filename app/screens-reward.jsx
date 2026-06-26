/* 累積里程 · 里程轉盤 · 九宮格集點卡 */

/* ---------- 累積里程 ---------- */
function MileageScreen({ nav }) {
  const D = window.DOR;
  const total = D.profile.totalKm;
  const earned = Math.floor(total / 50);
  const avail = earned - D.profile.spinsUsed;
  const toNext = 50 - (total % 50);
  const contrib = D.records.map(r => ({ t: r.title, km: r.dist }));

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop />
      <div style={{ padding: '0 18px', position: 'relative', zIndex: 2 }}>
        <div className="eyebrow">MILEAGE BANK</div>
        <div className="disp" style={{ fontSize: 26, fontWeight: 700, marginTop: 3 }}>累積里程</div>
      </div>

      {/* big total */}
      <div style={{ padding: '16px 18px 0', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 22, textAlign: 'center', background: 'radial-gradient(120% 120% at 50% 0, var(--fug-deep), var(--bg-2) 60%)', borderColor: 'rgba(45,229,154,.25)' }}>
          <div className="lbl">完賽累積里程</div>
          <div style={{ display: 'flex', alignItems: 'baseline', justifyContent: 'center', gap: 6, marginTop: 6 }}>
            <span className="num" style={{ fontSize: 64, lineHeight: 1, color: 'var(--fug)', textShadow: '0 0 32px rgba(45,229,154,.4)' }}>{total}</span>
            <span className="mono" style={{ fontSize: 18, color: 'var(--tx-dim)' }}>km</span>
          </div>
          <div style={{ marginTop: 18 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 12, color: 'var(--tx-dim)', marginBottom: 7 }}>
              <span className="mono">距離下一次轉盤</span><span className="mono tone-fug">還差 {toNext} km</span>
            </div>
            <Bar pct={(total % 50) / 50 * 100} glow />
            <div className="lbl" style={{ textAlign: 'left', marginTop: 6 }}>每累積 50 KM 可轉動轉盤一次</div>
          </div>
        </div>
      </div>

      {/* spins available */}
      <div style={{ padding: '14px 18px 0', position: 'relative', zIndex: 2 }}>
        <div className="card tap" onClick={() => nav('wheel', {})} style={{ padding: 16, display: 'flex', alignItems: 'center', gap: 14, borderColor: avail > 0 ? 'rgba(255,194,75,.35)' : 'var(--line)' }}>
          <div style={{ width: 52, height: 52, borderRadius: 14, background: 'rgba(255,194,75,.12)', border: '1px solid rgba(255,194,75,.3)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}>
            <Icon name="wheel" size={28} color="var(--gold)" />
          </div>
          <div style={{ flex: 1 }}>
            <div style={{ fontWeight: 700, fontSize: 16 }}>可轉動次數</div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 2 }}>{avail > 0 ? '立即前往里程轉盤' : '完成更多賽事里程解鎖'}</div>
          </div>
          <div className="num tone-gold" style={{ fontSize: 30 }}>{avail}</div>
        </div>
      </div>

      {/* contribution breakdown */}
      <SectionLabel>里程來源 ｜ 完賽賽事目標</SectionLabel>
      <div style={{ padding: '0 18px 20px', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 0, overflow: 'hidden' }}>
          {contrib.map((c, i) => (
            <div key={i} style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', padding: '13px 15px', borderTop: i ? '1px solid var(--line)' : 'none' }}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <Icon name="medal" size={18} color="var(--tx-dim)" />
                <span style={{ fontSize: 14 }}>{c.t}</span>
              </div>
              <span className="num tone-fug" style={{ fontSize: 15 }}>+{c.km} km</span>
            </div>
          ))}
        </div>
        <div className="lbl" style={{ marginTop: 10, lineHeight: 1.6 }}>累積里程以完賽的雲端賽事「里程目標」計算 — 42K 賽事累積 42、21K 賽事累積 21。</div>
      </div>
    </div>
  );
}

/* ---------- 里程轉盤 ---------- */
function WheelScreen({ back }) {
  const D = window.DOR;
  const pool = D.wheel;
  const n = pool.length;
  const seg = 360 / n;
  const colorOf = (c) => c === 'dim' ? 'var(--tx-faint)' : `var(--${c})`;
  const [rot, setRot] = React.useState(0);
  const [spinning, setSpinning] = React.useState(false);
  const [result, setResult] = React.useState(null);
  const [spins, setSpins] = React.useState(Math.floor(D.profile.totalKm / 50) - D.profile.spinsUsed);

  const conic = `conic-gradient(${pool.map((p, i) => {
    const col = colorOf(p.color);
    return `${col} ${i * seg}deg ${(i + 1) * seg}deg`;
  }).join(',')})`;

  const spin = () => {
    if (spinning || spins <= 0) return;
    setResult(null); setSpinning(true); setSpins(s => s - 1);
    // weighted pick
    const tot = pool.reduce((a, p) => a + p.weight, 0);
    let rnd = Math.random() * tot, idx = 0;
    for (let i = 0; i < n; i++) { rnd -= pool[i].weight; if (rnd <= 0) { idx = i; break; } }
    const target = 360 * 5 + (360 - (idx * seg + seg / 2));
    const base = rot - (rot % 360);
    setRot(base + target);
    setTimeout(() => { setSpinning(false); setResult(pool[idx]); if (pool[idx].kind === 'again') setSpins(s => s + 1); }, 4200);
  };

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop h={50} />
      <TopBar title="里程轉盤" onBack={back} right={<span className="chip gold" style={{ fontWeight: 700 }}>剩餘 {spins}</span>} />

      <div style={{ padding: '8px 18px 0', position: 'relative', zIndex: 2, display: 'flex', flexDirection: 'column', alignItems: 'center' }}>
        {/* pointer */}
        <div style={{ width: 0, height: 0, borderLeft: '12px solid transparent', borderRight: '12px solid transparent', borderTop: '20px solid var(--gold)', filter: 'drop-shadow(0 0 6px var(--gold))', zIndex: 3, marginBottom: -8 }} />
        {/* wheel */}
        <div style={{ position: 'relative', width: 288, height: 288 }}>
          <div style={{
            position: 'absolute', inset: 0, borderRadius: '50%', background: conic,
            transform: `rotate(${rot}deg)`, transition: spinning ? 'transform 4.2s cubic-bezier(.17,.67,.12,1)' : 'none',
            boxShadow: '0 0 0 6px var(--bg-3), 0 0 0 8px var(--line-2), 0 20px 50px rgba(0,0,0,.5)',
          }}>
            {/* segment labels */}
            {pool.map((p, i) => (
              <div key={i} style={{ position: 'absolute', left: '50%', top: '50%', transformOrigin: '0 0', transform: `rotate(${i * seg + seg / 2}deg) translate(0, -118px)`, }}>
                <div style={{ transform: 'translate(-50%,-50%) rotate(90deg)', width: 78, textAlign: 'center' }}>
                  <div className="disp" style={{ fontSize: 11, fontWeight: 700, color: p.color === 'gold' || p.color === 'dim' ? '#1a1306' : '#08120d', lineHeight: 1.15 }}>
                    {p.kind === 'line' ? `LINE\n${p.amount}` : p.kind === 'sticker' ? (p.amount > 1 ? '集點卡×2' : '集點卡') : p.label}
                  </div>
                </div>
              </div>
            ))}
          </div>
          {/* hub */}
          <div style={{ position: 'absolute', left: '50%', top: '50%', transform: 'translate(-50%,-50%)', width: 64, height: 64, borderRadius: '50%', background: 'var(--bg-3)', border: '2px solid var(--line-strong)', display: 'flex', alignItems: 'center', justifyContent: 'center', zIndex: 2 }}>
            <Icon name="bolt" size={26} color="var(--gold)" />
          </div>
        </div>

        <button className="btn btn-gold" style={{ marginTop: 26, maxWidth: 260 }} disabled={spinning || spins <= 0} onClick={spin}>
          {spinning ? '轉動中…' : spins > 0 ? <><Icon name="spin" size={18} color="#2a1d00" /> 轉動轉盤</> : '已無轉動次數'}
        </button>
        <div className="lbl" style={{ marginTop: 12 }}>獎勵為隨機取得 · LINE Points 與九宮格集點卡</div>
      </div>

      {/* odds */}
      <SectionLabel>獎項與機率</SectionLabel>
      <div style={{ padding: '0 18px 24px', position: 'relative', zIndex: 2, display: 'flex', flexDirection: 'column', gap: 8 }}>
        {pool.map((p, i) => {
          const tot = pool.reduce((a, x) => a + x.weight, 0);
          return (
            <div key={i} className="card" style={{ padding: '11px 14px', display: 'flex', alignItems: 'center', gap: 11 }}>
              <span style={{ width: 12, height: 12, borderRadius: 4, background: colorOf(p.color) }} />
              <span style={{ flex: 1, fontSize: 14 }}>{p.kind === 'line' ? `LINE Points ${p.amount}` : p.label}</span>
              <span className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{Math.round(p.weight / tot * 100)}%</span>
            </div>
          );
        })}
      </div>

      {/* result modal */}
      {result && <WheelResult result={result} onClose={() => setResult(null)} />}
    </div>
  );
}

function WheelResult({ result, onClose }) {
  const win = result.kind === 'line' || result.kind === 'sticker';
  const c = result.color === 'dim' ? 'var(--tx-dim)' : `var(--${result.color})`;
  return (
    <div onClick={onClose} style={{ position: 'absolute', inset: 0, zIndex: 40, background: 'rgba(5,6,9,.78)', backdropFilter: 'blur(6px)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 28 }}>
      <div className="card" onClick={e => e.stopPropagation()} style={{ width: '100%', maxWidth: 320, padding: 26, textAlign: 'center', borderColor: c, boxShadow: `0 0 40px ${c}55` }}>
        <div className="lbl">{win ? '恭喜獲得' : '結果'}</div>
        <div style={{ width: 86, height: 86, borderRadius: 22, margin: '16px auto', background: `${c}1f`, border: `1.5px solid ${c}`, display: 'flex', alignItems: 'center', justifyContent: 'center', boxShadow: `0 0 28px ${c}66` }}>
          <Icon name={result.kind === 'line' ? 'gift' : result.kind === 'sticker' ? 'id' : result.kind === 'again' ? 'spin' : 'bell'} size={40} color={c} />
        </div>
        <div className="disp" style={{ fontSize: 24, fontWeight: 700, color: c }}>
          {result.kind === 'line' ? `LINE Points ${result.amount}` : result.kind === 'sticker' ? `九宮格集點卡 ×${result.amount}` : result.label}
        </div>
        <div style={{ fontSize: 13, color: 'var(--tx-dim)', marginTop: 8 }}>
          {result.kind === 'line' ? '已存入你的 LINE 錢包' : result.kind === 'sticker' ? '已加入九宮格集點卡' : result.kind === 'again' ? '獲得一次額外轉動機會' : '再接再厲，下次一定中'}
        </div>
        <button className="btn btn-ghost" style={{ marginTop: 20 }} onClick={onClose}>收下</button>
      </div>
    </div>
  );
}

/* ---------- 九宮格集點卡 ---------- */
function StickerScreen({ back }) {
  const D = window.DOR;
  const cards = D.stickers;
  const got = cards.filter(c => c.got).length;
  const complete = got === cards.length;

  return (
    <div className="dor-scroll">
      <div className="dor-grid-tex" />
      <SafeTop h={50} />
      <TopBar title="九宮格集點卡" onBack={back} />

      <div style={{ padding: '4px 18px 0', position: 'relative', zIndex: 2 }}>
        <div style={{ display: 'flex', justifyContent: 'space-between', alignItems: 'flex-end' }}>
          <div>
            <div className="lbl">集滿九宮格 · 兌換完賽公仔</div>
            <div className="disp" style={{ fontSize: 30, fontWeight: 700, marginTop: 5 }}>{got}<span style={{ color: 'var(--tx-faint)', fontSize: 22 }}> / {cards.length}</span></div>
          </div>
          <span className="chip violet" style={{ fontWeight: 700 }}>已收集 {got}</span>
        </div>
        <div style={{ marginTop: 12 }}><Bar pct={got / cards.length * 100} color="var(--violet)" glow /></div>
      </div>

      {/* grid */}
      <div style={{ padding: '18px 18px 0', position: 'relative', zIndex: 2 }}>
        <div style={{ display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10 }}>
          {cards.map(c => (
            <div key={c.i} style={{
              aspectRatio: '1', borderRadius: 16, position: 'relative', overflow: 'hidden',
              border: '1px solid ' + (c.got ? 'rgba(157,140,255,.5)' : 'var(--line-2)'),
              background: c.got ? 'radial-gradient(120% 120% at 30% 20%, rgba(157,140,255,.22), var(--bg-2))' : 'var(--bg-2)',
              display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 7,
              boxShadow: c.got ? '0 0 18px rgba(157,140,255,.25)' : 'none',
            }}>
              <Icon name={c.got ? 'id' : 'lock'} size={26} color={c.got ? 'var(--violet)' : 'var(--tx-faint)'} />
              <div className="mono" style={{ fontSize: 11, color: c.got ? 'var(--tx)' : 'var(--tx-faint)' }}>{c.got ? c.name : '未取得'}</div>
              <div className="lbl" style={{ position: 'absolute', top: 8, left: 9, fontSize: 9 }}>{String(c.i).padStart(2, '0')}</div>
            </div>
          ))}
        </div>
      </div>

      {/* reward */}
      <SectionLabel>集滿獎勵</SectionLabel>
      <div style={{ padding: '0 18px 24px', position: 'relative', zIndex: 2 }}>
        <div className="card" style={{ padding: 16, display: 'flex', gap: 14, alignItems: 'center', borderColor: complete ? 'var(--violet)' : 'var(--line)' }}>
          <div className="ph" style={{ width: 76, height: 76, borderRadius: 14, flexShrink: 0 }}>公仔</div>
          <div style={{ flex: 1 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
              <span style={{ fontWeight: 700, fontSize: 16 }}>完賽公仔</span>
              <span className="chip violet" style={{ fontSize: 9, padding: '1px 7px' }}>特殊獎勵</span>
            </div>
            <div style={{ fontSize: 12.5, color: 'var(--tx-dim)', marginTop: 4, lineHeight: 1.5 }}>集滿九宮格全部 9 張集點卡，即可免費兌換限定完賽公仔。</div>
          </div>
        </div>
        <button className="btn" style={{ marginTop: 14, background: complete ? 'var(--violet)' : undefined, color: complete ? '#150f2e' : undefined }} disabled={!complete}>
          {complete ? '免費兌換完賽公仔' : `再集 ${cards.length - got} 張即可兌換`}
        </button>
      </div>
    </div>
  );
}

Object.assign(window, { MileageScreen, WheelScreen, WheelResult, StickerScreen });
