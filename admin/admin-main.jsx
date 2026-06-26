/* DOR 後台管理 console */
const { useState: aUse } = React;

const NAV = [
  { grp: '營運', items: [
    { k: 'dash',          t: '數據總覽', icon: 'target' },
    { k: 'races',         t: '賽事管理', icon: 'flag' },
    { k: 'signups',       t: '報名管理', icon: 'user' },
    { k: 'teams',         t: '跑團管理', icon: 'rescue' },
    { k: 'notifications', t: '推播通知', icon: 'bell' },
  ]},
  { grp: '遊戲設定', items: [
    { k: 'factions', t: '陣營設定',     icon: 'stealth' },
    { k: 'missions', t: '每日任務',     icon: 'chase' },
    { k: 'stores',   t: '打卡門市',     icon: 'pin' },
    { k: 'mileage',  t: '里程規則',     icon: 'route' },
    { k: 'wheel',    t: '轉盤獎勵',     icon: 'wheel' },
    { k: 'stickers', t: '集點卡 & 公仔', icon: 'id' },
  ]},
];

const TITLES = {
  dash:          ['數據總覽',    '出勤、完成率與戰局平衡'],
  races:         ['賽事管理',    '建立與編輯雲端賽事'],
  signups:       ['報名管理',    '參賽者與組別'],
  teams:         ['跑團管理',    '公會戰排名與跑團設定'],
  notifications: ['推播通知',    '晚間戰報排程與手動推播'],
  factions:      ['陣營設定',    '陣營分配方式、比例目標與手動調整'],
  missions:      ['每日任務',    '配置 7 日任務卡與救援規則'],
  stores:        ['打卡門市',    '補給站與打卡點'],
  mileage:       ['里程規則',    '里程銀行閾值、計算規則與積分概覽'],
  wheel:         ['轉盤獎勵',    '獎項與中獎機率設定'],
  stickers:      ['集點卡 & 公仔', '九宮格集點卡與完賽公仔兌換'],
};

function AdminApp() {
  const [page, setPage] = aUse('dash');
  const [t, sub] = TITLES[page];
  return (
    <div className="adm">
      <aside className="adm-side">
        <div className="adm-logo">
          <div className="mark"><Icon name="bolt" size={18} color="#05140e" /></div>
          <div>
            <div style={{ fontFamily: 'var(--font-disp)', fontWeight: 700, fontSize: 15 }}>DOR</div>
            <div className="lbl" style={{ fontSize: 8 }}>CONSOLE</div>
          </div>
        </div>
        <nav className="adm-nav">
          {NAV.map(g => (
            <React.Fragment key={g.grp}>
              <div className="grp">{g.grp}</div>
              {g.items.map(it => (
                <a key={it.k} className={page === it.k ? 'on' : ''} onClick={() => setPage(it.k)}>
                  <Icon name={it.icon} size={18} color={page === it.k ? 'var(--fug)' : 'var(--tx-dim)'} />
                  {it.t}
                </a>
              ))}
            </React.Fragment>
          ))}
        </nav>
        <div style={{ marginTop: 'auto', display: 'flex', alignItems: 'center', gap: 10, padding: '10px', borderTop: '1px solid var(--line)' }}>
          <div className="ph" style={{ width: 30, height: 30, borderRadius: 8 }}>OP</div>
          <div style={{ fontSize: 12.5 }}>營運後台<div className="lbl" style={{ fontSize: 8, marginTop: 1 }}>admin@dor.run</div></div>
        </div>
      </aside>

      <main className="adm-main">
        <header className="adm-top">
          <div style={{ flex: 1 }}>
            <div className="adm-h1" style={{ fontSize: 18 }}>{t}</div>
          </div>
          <div className="tag g"><span style={{ width: 6, height: 6, borderRadius: 9, background: 'var(--fug)', boxShadow: 'var(--glow-fug)' }} />獵人之夜 · 進行中</div>
          <button className="abtn abtn-gh"><Icon name="bell" size={16} /></button>
        </header>
        <div className="adm-body">
          <div style={{ marginBottom: 20 }}><div className="adm-sub">{sub}</div></div>
          {page === 'dash'          && <Dashboard />}
          {page === 'races'         && <RacesAdmin />}
          {page === 'signups'       && <SignupsAdmin />}
          {page === 'teams'         && <TeamsAdmin />}
          {page === 'notifications' && <NotificationsAdmin />}
          {page === 'factions'      && <FactionsAdmin />}
          {page === 'missions'      && <MissionsAdmin />}
          {page === 'stores'        && <StoresAdmin />}
          {page === 'mileage'       && <MileageAdmin />}
          {page === 'wheel'         && <WheelAdmin />}
          {page === 'stickers'      && <StickersAdmin />}
        </div>
      </main>
    </div>
  );
}

/* ---------------- Dashboard ---------------- */
function Dashboard() {
  const att = [62, 70, 58, 81, 74, 88, 92];
  const days = ['一', '二', '三', '四', '五', '六', '日'];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div className="kpi-grid">
        {[
          { l: '報名人數', v: '4,812', d: '+12.4% 週', c: 'var(--fug)' },
          { l: '今日出勤', v: '3,106', d: '64.5% 出勤率', c: 'var(--fug)' },
          { l: '任務完成率', v: '78%', d: '+6pt 對比上場', c: 'var(--gold)' },
          { l: '報名轉換', v: '31%', d: '等待名單 1,204', c: 'var(--violet)' },
        ].map((k, i) => (
          <div className="kpi" key={i}>
            <div className="l">{k.l}</div>
            <div className="v" style={{ color: k.c }}>{k.v}</div>
            <div className="d" style={{ color: 'var(--tx-dim)' }}>{k.d}</div>
          </div>
        ))}
      </div>

      <div style={{ display: 'grid', gridTemplateColumns: '1.5fr 1fr', gap: 20 }}>
        {/* attendance chart */}
        <div className="panel">
          <div className="panel-h"><span className="t">每日出勤趨勢</span><span className="tag">本賽事 7 日</span></div>
          <div style={{ padding: 20, display: 'flex', alignItems: 'flex-end', gap: 16, height: 220 }}>
            {att.map((v, i) => (
              <div key={i} style={{ flex: 1, display: 'flex', flexDirection: 'column', alignItems: 'center', gap: 8, height: '100%', justifyContent: 'flex-end' }}>
                <div style={{ width: '100%', maxWidth: 38, height: v + '%', borderRadius: '8px 8px 0 0', background: i === 2 ? 'var(--fug)' : 'rgba(45,229,154,.3)', boxShadow: i === 2 ? 'var(--glow-fug)' : 'none' }} />
                <span className="mono" style={{ fontSize: 11, color: i === 2 ? 'var(--fug)' : 'var(--tx-faint)' }}>{days[i]}</span>
              </div>
            ))}
          </div>
        </div>

        {/* faction balance */}
        <div className="panel">
          <div className="panel-h"><span className="t">陣營平衡</span><span className="tag g">健康</span></div>
          <div style={{ padding: 20 }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13, marginBottom: 8 }}>
              <span className="tone-fug">逃亡者 58%</span><span className="tone-hunt">42% 獵人</span>
            </div>
            <div style={{ height: 12, borderRadius: 999, overflow: 'hidden', display: 'flex', background: 'var(--hunt)' }}>
              <div style={{ width: '58%', background: 'var(--fug)', boxShadow: 'var(--glow-fug)' }} />
            </div>
            <div style={{ display: 'flex', flexDirection: 'column', gap: 12, marginTop: 20 }}>
              {[['今日救援觸發', '1,842', 'var(--fug)'], ['今日捕獲', '1,531', 'var(--hunt)'], ['身份卡分享', '624', 'var(--gold)'], ['LINE 互動', '8,210', 'var(--violet)']].map(([l, v, c], i) => (
                <div key={i} style={{ display: 'flex', justifyContent: 'space-between', fontSize: 13.5 }}>
                  <span style={{ color: 'var(--tx-dim)' }}>{l}</span>
                  <span className="num" style={{ color: c }}>{v}</span>
                </div>
              ))}
            </div>
          </div>
        </div>
      </div>

      {/* recent */}
      <div className="panel">
        <div className="panel-h"><span className="t">即時動態</span><span className="tag">自動更新</span></div>
        <table className="adm-tbl">
          <thead><tr><th>時間</th><th>跑者</th><th>事件</th><th>賽事</th><th>影響</th></tr></thead>
          <tbody>
            {[
              ['20:14', '陳逸帆', '完成救援任務 +2K', '獵人之夜', '釋放 2 位夥伴'],
              ['20:09', '林子澄', '高速追擊達標', '獵人之夜', '捕獲 +1'],
              ['20:02', '王思妤', '門市打卡 · 信義店', '獵人之夜', '+50 救援值'],
              ['19:58', '張耀文', '累積里程達 50K', '—', '獲得轉盤 ×1'],
            ].map((r, i) => (
              <tr key={i}>
                <td className="mono" style={{ color: 'var(--tx-dim)' }}>{r[0]}</td>
                <td style={{ fontWeight: 600 }}>{r[1]}</td>
                <td>{r[2]}</td>
                <td><span className="tag">{r[3]}</span></td>
                <td className="tone-fug" style={{ fontFamily: 'var(--font-mono)', fontSize: 12.5 }}>{r[4]}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

window.AdminApp = AdminApp;
window.Dashboard = Dashboard;
