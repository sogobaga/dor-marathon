/* DOR 後台 — 其餘管理頁 */

function StatusTag({ s }) {
  const m = { live: ['進行中', 'g'], open: ['報名中', 'y'], soon: ['即將開始', 'v'], done: ['已結束', ''] };
  const [t, c] = m[s] || m.done;
  return <span className={'tag ' + c}>{t}</span>;
}

/* ---------------- 賽事管理 ---------------- */
function RacesAdmin() {
  const [modal, setModal] = React.useState(false);
  const races = window.DOR.races;
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', justifyContent: 'flex-end' }}>
        <button className="abtn abtn-pri" onClick={() => setModal(true)}><Icon name="plus" size={16} color="#05140e" /> 新增賽事</button>
      </div>
      <div className="panel">
        <table className="adm-tbl">
          <thead><tr><th>賽事</th><th>世界觀</th><th>組別</th><th>賽期</th><th>隊伍</th><th>狀態</th><th></th></tr></thead>
          <tbody>
            {races.map(r => (
              <tr key={r.id}>
                <td><div style={{ fontWeight: 700 }}>{r.title}</div><div className="mono" style={{ fontSize: 10.5, color: 'var(--tx-faint)' }}>{r.sub}</div></td>
                <td style={{ color: 'var(--tx-dim)' }}>{r.world}</td>
                <td>
                  <div style={{ display: 'flex', flexDirection: 'column', gap: 4, alignItems: 'flex-start' }}>
                    {r.groupType === 'faction' ? (
                      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                        {r.factions?.map(f => <span key={f.id} className={'tag ' + (f.color === 'fug' ? 'g' : f.color === 'hunt' ? 'r' : 'v')} style={{ fontSize: 10, padding: '2px 7px' }}>{f.name}</span>)}
                      </div>
                    ) : r.groupType === 'club' ? (
                      <div style={{ display: 'flex', gap: 4, flexWrap: 'wrap' }}>
                        {r.clubs?.slice(0, 3).map(c => <span key={c.id} className="tag" style={{ fontSize: 10, padding: '2px 7px' }}>{c.name}</span>)}
                        {(r.clubs?.length || 0) > 3 && <span className="tag" style={{ fontSize: 10, padding: '2px 7px' }}>+{r.clubs.length - 3}</span>}
                      </div>
                    ) : (
                      <div style={{ display: 'flex', gap: 4 }}>
                        {r.distances.map(d => <span key={d} className="tag" style={{ fontSize: 10, padding: '2px 7px' }}>{d}K</span>)}
                      </div>
                    )}
                    {r.groupMode === 'random' && <span className="tag v" style={{ fontSize: 9, padding: '1px 6px' }}>隨機分組</span>}
                    {r.groupMode === 'self' && r.groupType !== 'distance' && <span className="tag" style={{ fontSize: 9, padding: '1px 6px' }}>自行選擇</span>}
                  </div>
                </td>
                <td className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{r.start}–{r.end}</td>
                <td className="num">{r.teams ? r.teams.toLocaleString() : '—'}</td>
                <td><StatusTag s={r.status} /></td>
                <td><button className="abtn abtn-gh" style={{ padding: '6px 10px' }}><Icon name="settings" size={14} /></button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {modal && <RaceModal onClose={() => setModal(false)} />}
    </div>
  );
}

function RaceModal({ onClose }) {
  const [world, setWorld] = React.useState('獵人 vs 逃亡者');
  const [groupType, setGroupType] = React.useState('faction');
  const [groupMode, setGroupMode] = React.useState('random');
  const [factions, setFactions] = React.useState([
    { id: 1, name: '逃亡者', color: 'fug' },
    { id: 2, name: '獵人',   color: 'hunt' },
  ]);
  const [clubs, setClubs] = React.useState([
    { id: 1, name: '' }, { id: 2, name: '' },
  ]);
  const [uid, setUid] = React.useState(3);

  const addFaction = () => { setFactions(f => [...f, { id: uid, name: '', color: 'violet' }]); setUid(n => n + 1); };
  const rmFaction  = id => setFactions(f => f.filter(x => x.id !== id));
  const updFaction = (id, k, v) => setFactions(f => f.map(x => x.id === id ? { ...x, [k]: v } : x));
  const addClub    = () => { setClubs(c => [...c, { id: uid, name: '' }]); setUid(n => n + 1); };
  const rmClub     = id => setClubs(c => c.filter(x => x.id !== id));
  const updClub    = (id, v) => setClubs(c => c.map(x => x.id === id ? { ...x, name: v } : x));

  const colorOpts = [{ k: 'fug', label: '綠（逃亡）' }, { k: 'hunt', label: '紅（獵人）' }, { k: 'violet', label: '紫' }, { k: 'gold', label: '金' }];
  const colorDot  = { fug: 'var(--fug)', hunt: 'var(--hunt)', violet: 'var(--violet)', gold: 'var(--gold)' };

  return (
    <div onClick={onClose} style={ovl}>
      <div onClick={e => e.stopPropagation()} className="panel" style={{ width: 590, maxHeight: '90vh', overflow: 'auto' }}>
        <div className="panel-h"><span className="t">新增賽事</span><button className="abtn abtn-gh" style={{ padding: '6px 9px' }} onClick={onClose}>✕</button></div>
        <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 16 }}>

          {/* 基本資訊 */}
          <div className="field"><label>賽事名稱</label><input defaultValue="" placeholder="例：獵人之夜" /></div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
            <div className="field"><label>英文代號</label><input placeholder="HUNTERS NIGHT" /></div>
            <div className="field"><label>世界觀</label>
              <select value={world} onChange={e => setWorld(e.target.value)}>
                <option>獵人 vs 逃亡者</option><option>跑團 vs 跑團</option><option>配速控制挑戰</option>
              </select>
            </div>
          </div>
          <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 14 }}>
            <div className="field"><label>開賽日</label><input type="date" /></div>
            <div className="field"><label>結束日</label><input type="date" /></div>
            <div className="field"><label>賽程天數</label><input type="number" defaultValue="7" /></div>
          </div>

          {/* ── 分組設定 ── */}
          <div style={{ border: '1px solid var(--line-2)', borderRadius: 12, padding: 16, display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div style={{ fontWeight: 700, fontSize: 12.5, color: 'var(--tx)', display: 'flex', alignItems: 'center', gap: 7 }}>
              <Icon name="id" size={14} color="var(--lavender)" /> 分組設定
            </div>

            {/* 分組類型 */}
            <div>
              <div className="lbl" style={{ marginBottom: 7 }}>分組類型</div>
              <div style={{ display: 'flex', gap: 8 }}>
                {[['distance', '距離組別'], ['faction', '陣營對戰'], ['club', '跑團競賽']].map(([k, lbl]) => (
                  <button key={k} className={'abtn ' + (groupType === k ? 'abtn-pri' : 'abtn-gh')}
                    onClick={() => setGroupType(k)} style={{ flex: 1, fontSize: 12.5 }}>{lbl}</button>
                ))}
              </div>
            </div>

            {/* 分組模式（距離組別不需要隨機） */}
            {groupType !== 'distance' && (
              <div>
                <div className="lbl" style={{ marginBottom: 7 }}>分組模式</div>
                <div style={{ display: 'flex', gap: 8 }}>
                  {[['self', '自行選擇'], ['random', '隨機分組']].map(([k, lbl]) => (
                    <button key={k} className={'abtn ' + (groupMode === k ? 'abtn-pri' : 'abtn-gh')}
                      onClick={() => setGroupMode(k)} style={{ flex: 1, fontSize: 12.5 }}>{lbl}</button>
                  ))}
                </div>
                {groupMode === 'random' && (
                  <div style={{ marginTop: 9, padding: '9px 12px', background: 'rgba(139,126,200,.08)', border: '1px solid rgba(139,126,200,.22)', borderRadius: 8, fontSize: 12, color: 'var(--lavender)', lineHeight: 1.55 }}>
                    ⚡ 開賽時系統隨機分配分組，報名者不會提前知道所屬{groupType === 'faction' ? '陣營' : '跑團'}。
                  </div>
                )}
              </div>
            )}

            {/* 距離組別內容 */}
            {groupType === 'distance' && (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                <div className="field"><label>距離組別 (km，逗號分隔)</label><input defaultValue="10, 21, 42" /></div>
                <div className="field"><label>各組報名費 (NT$，逗號分隔)</label><input defaultValue="490, 690, 890" /></div>
              </div>
            )}

            {/* 陣營對戰內容 */}
            {groupType === 'faction' && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div className="lbl">陣營列表</div>
                {factions.map((f, i) => (
                  <div key={f.id} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <div style={{ width: 8, height: 8, borderRadius: '50%', background: colorDot[f.color] || 'var(--tx-mid)', flexShrink: 0 }} />
                    <input value={f.name} onChange={e => updFaction(f.id, 'name', e.target.value)} placeholder={`陣營 ${i + 1} 名稱`} style={{ flex: 1 }} />
                    <select value={f.color} onChange={e => updFaction(f.id, 'color', e.target.value)} style={{ width: 120 }}>
                      {colorOpts.map(c => <option key={c.k} value={c.k}>{c.label}</option>)}
                    </select>
                    {factions.length > 2 && (
                      <button className="abtn abtn-gh" style={{ padding: '5px 8px', color: 'var(--coral)', fontSize: 12 }} onClick={() => rmFaction(f.id)}>✕</button>
                    )}
                  </div>
                ))}
                <button className="abtn abtn-gh" style={{ alignSelf: 'flex-start', fontSize: 12 }} onClick={addFaction}><Icon name="plus" size={13} /> 新增陣營</button>
                <div style={{ padding: '12px 14px', background: 'rgba(45,229,154,.06)', border: '1px solid rgba(45,229,154,.2)', borderRadius: 10 }}>
                  <div className="lbl" style={{ color: 'var(--fug)', marginBottom: 10, fontSize: 11 }}>陣營玩法參數</div>
                  <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr 1fr', gap: 12 }}>
                    <div className="field"><label>逃亡者目標比例 %</label><input type="number" defaultValue="55" /></div>
                    <div className="field"><label>救援倍率（救援日）</label><input type="number" defaultValue="2" /></div>
                    <div className="field"><label>晚間戰報時間</label><input type="time" defaultValue="20:00" /></div>
                  </div>
                </div>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                  <div className="field"><label>距離選項 (km，逗號分隔)</label><input defaultValue="10, 21, 42" /></div>
                  <div className="field"><label>各組報名費 (NT$，逗號分隔)</label><input defaultValue="490, 690, 890" /></div>
                </div>
              </div>
            )}

            {/* 跑團競賽內容 */}
            {groupType === 'club' && (
              <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
                <div className="lbl">跑團列表</div>
                {clubs.map((c, i) => (
                  <div key={c.id} style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <div style={{ width: 22, height: 22, borderRadius: 6, background: 'var(--surface-3)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center', fontSize: 11, fontWeight: 700, color: 'var(--tx-mid)', flexShrink: 0 }}>{String.fromCharCode(65 + i)}</div>
                    <input value={c.name} onChange={e => updClub(c.id, e.target.value)} placeholder={`跑團 ${i + 1} 名稱`} style={{ flex: 1 }} />
                    {clubs.length > 2 && (
                      <button className="abtn abtn-gh" style={{ padding: '5px 8px', color: 'var(--coral)', fontSize: 12 }} onClick={() => rmClub(c.id)}>✕</button>
                    )}
                  </div>
                ))}
                <button className="abtn abtn-gh" style={{ alignSelf: 'flex-start', fontSize: 12 }} onClick={addClub}><Icon name="plus" size={13} /> 新增跑團</button>
                <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                  <div className="field"><label>距離選項 (km，逗號分隔)</label><input defaultValue="10, 21, 42" /></div>
                  <div className="field"><label>各組報名費 (NT$，逗號分隔)</label><input defaultValue="490, 690, 890" /></div>
                </div>
              </div>
            )}
          </div>

          <div className="field"><label>賽事簡介</label><textarea rows="2" placeholder="一半的人追，一半的人逃…" style={{ resize: 'vertical' }} /></div>
          <div className="field"><label>主視覺</label>
            <div className="ph" style={{ height: 88, borderRadius: 10 }}>拖曳上傳主視覺 16:9</div>
          </div>
          <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end', marginTop: 4 }}>
            <button className="abtn abtn-gh" onClick={onClose}>取消</button>
            <button className="abtn abtn-pri" onClick={onClose}>建立賽事</button>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ---------------- 報名管理 ---------------- */
function SignupsAdmin() {
  const rows = [
    ['陳逸帆', '獵人之夜', 21, 'fug', '台北夜行者', 59, '進行中'],
    ['林子澄', '獵人之夜', 42, 'hunt', '南港獵殺者', 41, '進行中'],
    ['王思妤', '獵人之夜', 10, 'fug', '內湖逃逸線', 88, '進行中'],
    ['張耀文', '獵人之夜', 21, 'hunt', '南港獵殺者', 33, '進行中'],
    ['黃詠晴', '城市公會戰', 42, null, '—', 0, '已報名'],
    ['周冠宇', '城市公會戰', 21, null, '—', 0, '已報名'],
  ];
  const [f, setF] = React.useState('all');
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
      <div style={{ display: 'flex', gap: 8 }}>
        {[['all', '全部 4,812'], ['fug', '逃亡者 2,790'], ['hunt', '獵人 2,022']].map(([k, t]) => (
          <button key={k} className={'abtn ' + (f === k ? 'abtn-pri' : 'abtn-gh')} onClick={() => setF(k)}>{t}</button>
        ))}
        <div style={{ flex: 1 }} />
        <button className="abtn abtn-gh"><Icon name="arrowUp" size={15} /> 匯出 CSV</button>
      </div>
      <div className="panel">
        <table className="adm-tbl">
          <thead><tr><th>跑者</th><th>賽事</th><th>組別</th><th>陣營</th><th>跑團</th><th>進度</th><th>狀態</th></tr></thead>
          <tbody>
            {rows.filter(r => f === 'all' || r[3] === f).map((r, i) => (
              <tr key={i}>
                <td style={{ fontWeight: 600 }}>{r[0]}</td>
                <td style={{ color: 'var(--tx-dim)' }}>{r[1]}</td>
                <td className="mono">{r[2]}K</td>
                <td>{r[3] ? <span className={'tag ' + (r[3] === 'fug' ? 'g' : 'r')}>{r[3] === 'fug' ? '逃亡者' : '獵人'}</span> : <span className="tag">未分配</span>}</td>
                <td style={{ color: 'var(--tx-dim)' }}>{r[4]}</td>
                <td><div style={{ display: 'flex', alignItems: 'center', gap: 8 }}><div className="bar" style={{ width: 70 }}><i style={{ width: r[5] + '%', background: 'var(--fug)' }} /></div><span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{r[5]}%</span></div></td>
                <td><span className="tag g">{r[6]}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ---------------- 每日任務 ---------------- */
function MissionsAdmin() {
  const [missions, setMissions] = React.useState(window.DOR.missions.map(m => ({ ...m })));
  const upd = (day, key, val) => setMissions(ms => ms.map(m => m.day === day ? { ...m, [key]: val } : m));
  const types = { base: '里程任務', pace: '配速任務', rescue: '救援任務' };
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 14 }}>
      <div className="tag y" style={{ alignSelf: 'flex-start' }}>獵人之夜 · 7 日任務卡</div>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
        {missions.map(m => (
          <div key={m.day} className="panel" style={{ padding: 16 }}>
            <div style={{ display: 'flex', alignItems: 'center', gap: 10, marginBottom: 14 }}>
              <div style={{ width: 34, height: 34, borderRadius: 10, background: 'rgba(255,255,255,.04)', border: '1px solid var(--line-2)', display: 'flex', alignItems: 'center', justifyContent: 'center' }}><Icon name={m.icon} size={18} color="var(--fug)" /></div>
              <div style={{ flex: 1 }}>
                <div className="lbl" style={{ fontSize: 9 }}>DAY {m.day} · {m.tag}</div>
                <input value={m.title} onChange={e => upd(m.day, 'title', e.target.value)} style={titleInput} />
              </div>
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <div className="field"><label>任務類型</label>
                <select value={m.type} onChange={e => upd(m.day, 'type', e.target.value)}>{Object.entries(types).map(([k, v]) => <option key={k} value={k}>{v}</option>)}</select>
              </div>
              <div className="field"><label>基礎里程 (km)</label><input type="number" step="0.5" value={m.base} onChange={e => upd(m.day, 'base', +e.target.value)} /></div>
            </div>
            {m.type === 'pace' && (
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 12 }}>
                <div className="field"><label>配速下限 /km</label><input value={m.paceLo || ''} onChange={e => upd(m.day, 'paceLo', e.target.value)} placeholder="4:30" /></div>
                <div className="field"><label>配速上限 /km</label><input value={m.paceHi || ''} onChange={e => upd(m.day, 'paceHi', e.target.value)} placeholder="5:30" /></div>
              </div>
            )}
            <div style={{ marginTop: 12, display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12 }}>
              <div className="field">
                <label>救援倍率</label>
                <select defaultValue={m.type === 'rescue' ? '2' : '1'}>
                  <option value="1">×1（標準）</option>
                  <option value="2">×2（加倍）</option>
                  <option value="3">×3（三倍）</option>
                </select>
              </div>
              <div className="field">
                <label>推播提醒</label>
                <select defaultValue="auto">
                  <option value="auto">自動（賽事開始時）</option>
                  <option value="manual">手動推播</option>
                  <option value="off">關閉</option>
                </select>
              </div>
            </div>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginTop: 12, padding: '10px 13px', background: 'rgba(45,229,154,.06)', borderRadius: 10, border: '1px solid rgba(45,229,154,.2)' }}>
              <span style={{ fontSize: 13, display: 'flex', alignItems: 'center', gap: 7 }}><Icon name="rescue" size={15} color="var(--fug)" /> 多跑 1K 救援 1 人</span>
              <span className="tag g">{m.type === 'rescue' ? '加倍 ×2' : '啟用'}</span>
            </div>
          </div>
        ))}
      </div>
    </div>
  );
}

/* ---------------- 打卡門市 ---------------- */
function StoresAdmin() {
  const stores = window.DOR.stores;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 320px', gap: 20 }}>
      <div className="panel">
        <div className="panel-h"><span className="t">補給門市 ({stores.length})</span><button className="abtn abtn-pri" style={{ padding: '7px 12px' }}><Icon name="plus" size={14} color="#05140e" /> 新增門市</button></div>
        <table className="adm-tbl">
          <thead><tr><th>門市</th><th>城市</th><th>營業時間</th><th>打卡數</th><th>狀態</th></tr></thead>
          <tbody>
            {stores.map((s, i) => (
              <tr key={s.id}>
                <td><div style={{ fontWeight: 600 }}>{s.name}</div><div style={{ fontSize: 11.5, color: 'var(--tx-faint)' }}>{s.addr}</div></td>
                <td style={{ color: 'var(--tx-dim)' }}>{s.city}</td>
                <td className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}>{s.hours}</td>
                <td className="num">{[842, 531, 410, 233][i]}</td>
                <td><span className="tag g">啟用中</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="panel" style={{ alignSelf: 'start' }}>
        <div className="panel-h"><span className="t">門市分佈</span></div>
        <div className="ph" style={{ height: 240, borderRadius: 0, position: 'relative' }}>地圖
          {[[30, 35], [60, 28], [45, 62], [76, 70]].map(([l, t], i) => <div key={i} style={{ position: 'absolute', left: l + '%', top: t + '%', transform: 'translate(-50%,-100%)' }}><Icon name="pin" size={24} color="var(--violet)" /></div>)}
        </div>
        <div style={{ padding: 16 }}>
          <div className="field"><label>打卡獎勵設定</label><input defaultValue="+50 救援值" /></div>
        </div>
      </div>
    </div>
  );
}

/* ---------------- 轉盤獎勵 ---------------- */
function WheelAdmin() {
  const [pool, setPool] = React.useState(window.DOR.wheel.map(p => ({ ...p })));
  const tot = pool.reduce((a, p) => a + p.weight, 0);
  const setW = (id, w) => setPool(ps => ps.map(p => p.id === id ? { ...p, weight: +w } : p));
  const colorOf = (c) => c === 'dim' ? 'var(--tx-faint)' : `var(--${c})`;
  return (
    <div style={{ display: 'grid', gridTemplateColumns: '1fr 300px', gap: 20 }}>
      <div className="panel">
        <div className="panel-h"><span className="t">獎項與中獎機率</span><button className="abtn abtn-pri" style={{ padding: '7px 12px' }}><Icon name="plus" size={14} color="#05140e" /> 新增獎項</button></div>
        <div style={{ padding: '8px 18px 18px' }}>
          {pool.map(p => (
            <div key={p.id} style={{ display: 'flex', alignItems: 'center', gap: 14, padding: '13px 0', borderBottom: '1px solid var(--line)' }}>
              <span style={{ width: 12, height: 12, borderRadius: 4, background: colorOf(p.color), flexShrink: 0 }} />
              <div style={{ width: 150, flexShrink: 0 }}>
                <div style={{ fontWeight: 600, fontSize: 13.5 }}>{p.kind === 'line' ? `LINE Points ${p.amount}` : p.label}</div>
                <div className="lbl" style={{ fontSize: 8.5, marginTop: 2 }}>{p.kind === 'line' ? 'LINE POINT' : p.kind === 'sticker' ? '九宮格集點卡' : p.kind.toUpperCase()}</div>
              </div>
              <input type="range" min="0" max="40" value={p.weight} onChange={e => setW(p.id, e.target.value)} style={{ flex: 1, accentColor: colorOf(p.color) }} />
              <span className="num" style={{ width: 48, textAlign: 'right', color: colorOf(p.color) }}>{Math.round(p.weight / tot * 100)}%</span>
            </div>
          ))}
          <div className="lbl" style={{ marginTop: 14 }}>機率依權重自動正規化 · 總和恆為 100%</div>
        </div>
      </div>
      <div className="panel" style={{ alignSelf: 'start' }}>
        <div className="panel-h"><span className="t">即時預覽</span></div>
        <div style={{ padding: 24, display: 'flex', justifyContent: 'center' }}>
          <div style={{ width: 180, height: 180, borderRadius: '50%', background: `conic-gradient(${pool.map((p, i) => { const s = pool.slice(0, i).reduce((a, x) => a + x.weight, 0) / tot * 360; const e = s + p.weight / tot * 360; return `${colorOf(p.color)} ${s}deg ${e}deg`; }).join(',')})`, boxShadow: '0 0 0 5px var(--bg-3), 0 10px 30px rgba(0,0,0,.5)' }} />
        </div>
        <div style={{ padding: '0 18px 18px' }}>
          <div className="field"><label>觸發條件</label><input defaultValue="每累積 50 km 轉動 1 次" /></div>
        </div>
      </div>
    </div>
  );
}

/* ---------------- 集點卡 & 公仔 ---------------- */
function StickersAdmin() {
  const cards = window.DOR.stickers;
  const queue = [
    ['林子澄', '南港獵殺者', '2026.06.10', '備貨中', 'y'],
    ['王思妤', '內湖逃逸線', '2026.06.08', '已出貨', 'g'],
    ['周冠宇', '台北夜行者', '2026.06.05', '已領取', ''],
  ];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <div className="panel">
          <div className="panel-h"><span className="t">九宮格集點卡</span><span className="tag v">9 張一組</span></div>
          <div style={{ padding: 18, display: 'grid', gridTemplateColumns: 'repeat(3,1fr)', gap: 10 }}>
            {cards.map(c => (
              <div key={c.i} style={{ aspectRatio: '1', borderRadius: 12, border: '1px solid rgba(157,140,255,.3)', background: 'radial-gradient(120% 120% at 30% 20%, rgba(157,140,255,.14), var(--bg-1))', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 6 }}>
                <Icon name="id" size={22} color="var(--violet)" />
                <div className="mono" style={{ fontSize: 10 }}>{c.name}</div>
                <div className="lbl" style={{ fontSize: 8 }}>{String(c.i).padStart(2, '0')}</div>
              </div>
            ))}
          </div>
        </div>
        <div className="panel" style={{ alignSelf: 'start' }}>
          <div className="panel-h"><span className="t">完賽公仔</span><span className="tag v">特殊獎勵</span></div>
          <div style={{ padding: 18, display: 'flex', gap: 14, alignItems: 'center' }}>
            <div className="ph" style={{ width: 92, height: 92, borderRadius: 14 }}>公仔 3D</div>
            <div style={{ flex: 1, fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.6 }}>集滿 9 張集點卡可免費兌換。本季已兌換 <span className="num tone-violet">312</span> 座。</div>
          </div>
          <div style={{ padding: '0 18px 18px', display: 'flex', flexDirection: 'column', gap: 12 }}>
            <div className="field"><label>公仔名稱</label><input defaultValue="逃亡者・限定完賽公仔" /></div>
            <div className="field"><label>庫存</label><input type="number" defaultValue="500" /></div>
          </div>
        </div>
      </div>
      <div className="panel">
        <div className="panel-h"><span className="t">公仔兌換隊列</span><span className="tag y">待處理 1</span></div>
        <table className="adm-tbl">
          <thead><tr><th>跑者</th><th>跑團</th><th>集滿日期</th><th>出貨狀態</th><th></th></tr></thead>
          <tbody>
            {queue.map((q, i) => (
              <tr key={i}>
                <td style={{ fontWeight: 600 }}>{q[0]}</td>
                <td style={{ color: 'var(--tx-dim)' }}>{q[1]}</td>
                <td className="mono" style={{ fontSize: 12 }}>{q[2]}</td>
                <td><span className={'tag ' + q[4]}>{q[3]}</span></td>
                <td><button className="abtn abtn-gh" style={{ padding: '6px 11px' }}>{q[3] === '備貨中' ? '標記出貨' : '查看'}</button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ---------------- 陣營設定 ---------------- */
function FactionsAdmin() {
  const [method, setMethod] = React.useState('random');
  const [fugTarget, setFugTarget] = React.useState(55);
  const huntTarget = 100 - fugTarget;
  const races = window.DOR.races.filter(r => r.world === '獵人 vs 逃亡者');
  const reassignRows = [
    ['陳逸帆', '獵人之夜', '逃亡者', '台北夜行者'],
    ['林子澄', '獵人之夜', '獵人',   '南港獵殺者'],
    ['王思妤', '獵人之夜', '逃亡者', '內湖逃逸線'],
    ['張耀文', '獵人之夜', '獵人',   '南港獵殺者'],
  ];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* assignment method */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <div className="panel">
          <div className="panel-h"><span className="t">分配方式</span></div>
          <div style={{ padding: '14px 18px 18px', display: 'flex', flexDirection: 'column', gap: 10 }}>
            {[['random', '隨機分配', '開賽時系統依目標比例自動分配'],
              ['choice', '玩家自選', '報名時讓玩家自行選擇陣營'],
              ['admin',  '後台指定', '由營運人員手動指派每位參賽者'],
            ].map(([k, t, d]) => (
              <label key={k} style={{ display: 'flex', gap: 12, alignItems: 'flex-start', cursor: 'pointer', padding: '12px 14px', borderRadius: 10, border: '1px solid ' + (method === k ? 'var(--fug)' : 'var(--line)'), background: method === k ? 'rgba(45,229,154,.07)' : 'transparent' }}>
                <input type="radio" name="faction-method" value={k} checked={method === k} onChange={() => setMethod(k)} style={{ marginTop: 3, accentColor: 'var(--fug)' }} />
                <div>
                  <div style={{ fontWeight: 700, fontSize: 14 }}>{t}</div>
                  <div className="lbl" style={{ marginTop: 2 }}>{d}</div>
                </div>
              </label>
            ))}
          </div>
        </div>
        <div className="panel" style={{ alignSelf: 'start' }}>
          <div className="panel-h"><span className="t">比例目標</span><span className="tag g">健康範圍 45–65%</span></div>
          <div style={{ padding: '14px 18px 18px' }}>
            <div style={{ display: 'flex', justifyContent: 'space-between', marginBottom: 10 }}>
              <span className="mono" style={{ color: 'var(--fug)', fontSize: 13 }}>逃亡者 {fugTarget}%</span>
              <span className="mono" style={{ color: 'var(--hunt)', fontSize: 13 }}>{huntTarget}% 獵人</span>
            </div>
            <div style={{ height: 10, borderRadius: 999, overflow: 'hidden', display: 'flex', background: 'var(--hunt)', marginBottom: 14 }}>
              <div style={{ width: fugTarget + '%', background: 'var(--fug)', boxShadow: 'var(--glow-fug)', transition: 'width .2s' }} />
            </div>
            <div className="field">
              <label>逃亡者目標比例 (%)</label>
              <input type="range" min="30" max="70" value={fugTarget} onChange={e => setFugTarget(+e.target.value)} style={{ accentColor: 'var(--fug)' }} />
            </div>
            <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 12, marginTop: 14 }}>
              <div style={{ textAlign: 'center', padding: '10px 0', borderRadius: 10, border: '1px solid rgba(45,229,154,.3)', background: 'rgba(45,229,154,.06)' }}>
                <div className="num" style={{ color: 'var(--fug)', fontSize: 26 }}>2,790</div>
                <div className="lbl" style={{ marginTop: 3 }}>逃亡者（現況）</div>
              </div>
              <div style={{ textAlign: 'center', padding: '10px 0', borderRadius: 10, border: '1px solid rgba(255,75,92,.3)', background: 'rgba(255,75,92,.06)' }}>
                <div className="num" style={{ color: 'var(--hunt)', fontSize: 26 }}>2,022</div>
                <div className="lbl" style={{ marginTop: 3 }}>獵人（現況）</div>
              </div>
            </div>
          </div>
        </div>
      </div>

      {/* per-race balance */}
      <div className="panel">
        <div className="panel-h"><span className="t">各賽事陣營分佈</span></div>
        <table className="adm-tbl">
          <thead><tr><th>賽事</th><th>逃亡者</th><th>獵人</th><th>比例</th><th>健康度</th></tr></thead>
          <tbody>
            {races.map(r => {
              const fug = r.id === 'hunt2026' ? 58 : 52;
              const hunt = 100 - fug;
              const ok = fug >= 45 && fug <= 65;
              return (
                <tr key={r.id}>
                  <td style={{ fontWeight: 600 }}>{r.title}</td>
                  <td><span className="mono tone-fug">{fug}%</span></td>
                  <td><span className="mono tone-hunt">{hunt}%</span></td>
                  <td style={{ width: 140 }}>
                    <div className="bar" style={{ width: 120 }}>
                      <i style={{ width: fug + '%', background: 'var(--fug)' }} />
                    </div>
                  </td>
                  <td><span className={'tag ' + (ok ? 'g' : 'r')}>{ok ? '健康' : '失衡'}</span></td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>

      {/* manual reassign */}
      <div className="panel">
        <div className="panel-h"><span className="t">手動調整陣營</span><span className="tag y">謹慎操作</span></div>
        <table className="adm-tbl">
          <thead><tr><th>跑者</th><th>賽事</th><th>目前陣營</th><th>跑團</th><th></th></tr></thead>
          <tbody>
            {reassignRows.map((r, i) => (
              <tr key={i}>
                <td style={{ fontWeight: 600 }}>{r[0]}</td>
                <td style={{ color: 'var(--tx-dim)' }}>{r[1]}</td>
                <td><span className={'tag ' + (r[2] === '逃亡者' ? 'g' : 'r')}>{r[2]}</span></td>
                <td style={{ color: 'var(--tx-dim)' }}>{r[3]}</td>
                <td>
                  <select style={{ background: 'var(--bg-2)', color: 'var(--tx)', border: '1px solid var(--line-2)', borderRadius: 6, padding: '4px 8px', fontSize: 12 }} defaultValue={r[2]}>
                    <option>逃亡者</option>
                    <option>獵人</option>
                  </select>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
        <div style={{ padding: '12px 18px' }}>
          <button className="abtn abtn-pri" style={{ padding: '8px 18px' }}>儲存調整</button>
        </div>
      </div>
    </div>
  );
}

/* ---------------- 跑團管理 ---------------- */
function TeamsAdmin() {
  const [modal, setModal] = React.useState(false);
  const [scoring, setScoring] = React.useState('completion');
  const teams = [
    { name: '南港獵殺者', city: '台北', members: 48, km: 2142, rate: 94, rank: 1 },
    { name: '台北夜行者', city: '台北', members: 52, km: 1988, rate: 88, rank: 2 },
    { name: '內湖逃逸線', city: '台北', members: 37, km: 1654, rate: 81, rank: 3 },
    { name: '板橋暗夜跑者', city: '新北', members: 29, km: 1210, rate: 74, rank: 4 },
  ];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* guild war settings */}
      <div className="panel">
        <div className="panel-h"><span className="t">公會戰評分設定</span><span className="tag g">獵人之夜</span></div>
        <div style={{ padding: '14px 18px 18px', display: 'flex', flexDirection: 'column', gap: 14 }}>
          <div className="lbl" style={{ fontSize: 11 }}>排行計算方式</div>
          <div style={{ display: 'flex', gap: 10 }}>
            {[['completion', '任務完成率', '依每日任務達成比例加總'],
              ['rescue',     '救援總計',   '依全體成員救援次數加總'],
              ['km',         '累積里程',   '依全體成員公里數加總'],
            ].map(([k, t, d]) => (
              <label key={k} style={{ flex: 1, display: 'flex', flexDirection: 'column', gap: 4, cursor: 'pointer', padding: '12px 14px', borderRadius: 10, border: '1px solid ' + (scoring === k ? 'var(--fug)' : 'var(--line)'), background: scoring === k ? 'rgba(45,229,154,.07)' : 'transparent' }}>
                <div style={{ display: 'flex', gap: 8, alignItems: 'center' }}>
                  <input type="radio" name="scoring" value={k} checked={scoring === k} onChange={() => setScoring(k)} style={{ accentColor: 'var(--fug)' }} />
                  <span style={{ fontWeight: 700, fontSize: 13.5 }}>{t}</span>
                </div>
                <span className="lbl" style={{ paddingLeft: 20 }}>{d}</span>
              </label>
            ))}
          </div>
        </div>
      </div>

      {/* team list */}
      <div className="panel">
        <div className="panel-h">
          <span className="t">跑團列表</span>
          <button className="abtn abtn-pri" style={{ padding: '7px 12px' }} onClick={() => setModal(true)}><Icon name="plus" size={14} color="#05140e" /> 新增跑團</button>
        </div>
        <table className="adm-tbl">
          <thead><tr><th>排名</th><th>跑團</th><th>城市</th><th>成員數</th><th>累積里程</th><th>完成率</th><th></th></tr></thead>
          <tbody>
            {teams.map(t => (
              <tr key={t.name}>
                <td className="num" style={{ color: t.rank === 1 ? 'var(--gold)' : 'var(--tx-dim)', fontSize: 16 }}>{t.rank}</td>
                <td style={{ fontWeight: 600 }}>{t.name}</td>
                <td style={{ color: 'var(--tx-dim)' }}>{t.city}</td>
                <td className="num">{t.members}</td>
                <td><span className="mono tone-fug">{t.km.toLocaleString()} km</span></td>
                <td>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                    <div className="bar" style={{ width: 70 }}><i style={{ width: t.rate + '%', background: 'var(--fug)' }} /></div>
                    <span className="mono" style={{ fontSize: 11, color: 'var(--tx-dim)' }}>{t.rate}%</span>
                  </div>
                </td>
                <td><button className="abtn abtn-gh" style={{ padding: '5px 10px' }}><Icon name="settings" size={13} /></button></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      {modal && (
        <div onClick={() => setModal(false)} style={ovl}>
          <div onClick={e => e.stopPropagation()} className="panel" style={{ width: 420 }}>
            <div className="panel-h"><span className="t">新增跑團</span><button className="abtn abtn-gh" style={{ padding: '6px 9px' }} onClick={() => setModal(false)}>✕</button></div>
            <div style={{ padding: 20, display: 'flex', flexDirection: 'column', gap: 14 }}>
              <div className="field"><label>跑團名稱</label><input placeholder="例：南港獵殺者" /></div>
              <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 14 }}>
                <div className="field"><label>城市</label><input placeholder="台北" /></div>
                <div className="field"><label>隊長帳號</label><input placeholder="@handle" /></div>
              </div>
              <div style={{ display: 'flex', gap: 10, justifyContent: 'flex-end' }}>
                <button className="abtn abtn-gh" onClick={() => setModal(false)}>取消</button>
                <button className="abtn abtn-pri" onClick={() => setModal(false)}>建立跑團</button>
              </div>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}

/* ---------------- 推播通知 ---------------- */
function NotificationsAdmin() {
  const [reportTime, setReportTime] = React.useState('20:00');
  const [reportOn, setReportOn] = React.useState(true);
  const [draft, setDraft] = React.useState('');
  const history = [
    { time: '06/15 20:00', type: '晚間戰報', msg: '今日捕獲 531 · 逃脫 742 · 明日任務：誘餌與搜索', sent: 4812, open: '68%' },
    { time: '06/14 20:00', type: '晚間戰報', msg: '高速追擊達標者 384 人 · 明日救援日，倍率 ×2', sent: 4789, open: '71%' },
    { time: '06/13 09:00', type: '手動推播', msg: '補給站・板橋門市今日 12:00 起開放打卡', sent: 1240, open: '55%' },
  ];
  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* battle report schedule */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <div className="panel">
          <div className="panel-h"><span className="t">晚間戰報設定</span><span className={'tag ' + (reportOn ? 'g' : '')}>{reportOn ? '啟用中' : '已停用'}</span></div>
          <div style={{ padding: '14px 18px 18px', display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div className="field">
              <label>每日推播時間</label>
              <input type="time" value={reportTime} onChange={e => setReportTime(e.target.value)} />
            </div>
            <div className="field">
              <label>推播標題模板</label>
              <input defaultValue="DOR 戰報 · DAY {day} 結算" />
            </div>
            <div className="field">
              <label>內文模板</label>
              <textarea rows="3" defaultValue="今日捕獲 {captured} · 逃脫 {escaped}。明日任務：{next_mission}" style={{ resize: 'vertical' }} />
            </div>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span style={{ fontSize: 13.5 }}>自動推播</span>
              <button className={'abtn ' + (reportOn ? 'abtn-pri' : 'abtn-gh')} style={{ padding: '6px 14px' }} onClick={() => setReportOn(v => !v)}>
                {reportOn ? '啟用中' : '已停用'}
              </button>
            </div>
          </div>
        </div>

        {/* manual broadcast */}
        <div className="panel" style={{ alignSelf: 'start' }}>
          <div className="panel-h"><span className="t">手動推播</span></div>
          <div style={{ padding: '14px 18px 18px', display: 'flex', flexDirection: 'column', gap: 14 }}>
            <div className="field">
              <label>推播對象</label>
              <select>
                <option>全體參賽者 (4,812)</option>
                <option>逃亡者 (2,790)</option>
                <option>獵人 (2,022)</option>
                <option>今日未出勤者</option>
              </select>
            </div>
            <div className="field">
              <label>訊息內容</label>
              <textarea rows="4" value={draft} onChange={e => setDraft(e.target.value)} placeholder="輸入推播訊息…" style={{ resize: 'vertical' }} />
            </div>
            <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between' }}>
              <span className="lbl">{draft.length} / 150 字</span>
              <button className="abtn abtn-pri" style={{ padding: '8px 16px' }} disabled={!draft.trim()}>立即推播</button>
            </div>
          </div>
        </div>
      </div>

      {/* notification history */}
      <div className="panel">
        <div className="panel-h"><span className="t">推播紀錄</span></div>
        <table className="adm-tbl">
          <thead><tr><th>時間</th><th>類型</th><th>內容摘要</th><th>發送數</th><th>開啟率</th></tr></thead>
          <tbody>
            {history.map((h, i) => (
              <tr key={i}>
                <td className="mono" style={{ color: 'var(--tx-dim)', fontSize: 12 }}>{h.time}</td>
                <td><span className={'tag ' + (h.type === '晚間戰報' ? 'g' : 'v')}>{h.type}</span></td>
                <td style={{ color: 'var(--tx-dim)', maxWidth: 280 }}>{h.msg}</td>
                <td className="num">{h.sent.toLocaleString()}</td>
                <td className="mono tone-fug">{h.open}</td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ---------------- 里程規則 ---------------- */
function MileageAdmin() {
  const [threshold, setThreshold] = React.useState(50);
  const D = window.DOR;
  const top = [
    { name: '陳逸帆', team: '台北夜行者', km: 94, spins: 1, used: 0 },
    { name: '林子澄', team: '南港獵殺者', km: 157, spins: 3, used: 2 },
    { name: '王思妤', team: '內湖逃逸線', km: 231, spins: 4, used: 4 },
    { name: '張耀文', team: '南港獵殺者', km: 105, spins: 2, used: 1 },
  ].sort((a, b) => b.km - a.km);

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 20 }}>
      {/* rules config */}
      <div style={{ display: 'grid', gridTemplateColumns: '1fr 1fr', gap: 20 }}>
        <div className="panel">
          <div className="panel-h"><span className="t">里程銀行規則</span></div>
          <div style={{ padding: '14px 18px 18px', display: 'flex', flexDirection: 'column', gap: 16 }}>
            <div className="field">
              <label>每累積幾公里獲得一次轉盤機會</label>
              <div style={{ display: 'flex', alignItems: 'center', gap: 14, marginTop: 8 }}>
                <input type="range" min="10" max="100" step="5" value={threshold} onChange={e => setThreshold(+e.target.value)} style={{ flex: 1, accentColor: 'var(--fug)' }} />
                <span className="num tone-fug" style={{ fontSize: 22, minWidth: 60, textAlign: 'right' }}>{threshold}<span className="mono" style={{ fontSize: 12, color: 'var(--tx-dim)' }}> km</span></span>
              </div>
            </div>
            <div className="field">
              <label>里程計算來源</label>
              <select defaultValue="goal">
                <option value="goal">完賽目標距離（10K / 21K / 42K）</option>
                <option value="actual">實際跑步里程</option>
                <option value="both">兩者取較大值</option>
              </select>
            </div>
            <div className="field">
              <label>是否跨賽事累積</label>
              <select defaultValue="yes">
                <option value="yes">是（全部賽事合計）</option>
                <option value="no">否（各賽事獨立計算）</option>
              </select>
            </div>
            <div style={{ padding: '12px 14px', background: 'rgba(255,194,75,.07)', border: '1px solid rgba(255,194,75,.25)', borderRadius: 10, fontSize: 13, color: 'var(--tx-dim)', lineHeight: 1.65 }}>
              目前設定：完賽任一賽事後，依報名組別里程（42K → 42km）計入銀行。每累積 <span className="num tone-gold">{threshold}</span> km 解鎖一次轉盤。
            </div>
            <button className="abtn abtn-pri" style={{ alignSelf: 'flex-start', padding: '8px 20px' }}>儲存規則</button>
          </div>
        </div>

        {/* bank overview stats */}
        <div style={{ display: 'flex', flexDirection: 'column', gap: 16 }}>
          {[
            { l: '全平台累積里程', v: '428,610 km', c: 'var(--fug)' },
            { l: '已發放轉盤次數', v: '8,572 次', c: 'var(--gold)' },
            { l: '已使用轉盤次數', v: '6,134 次', c: 'var(--hunt)' },
            { l: '未使用（剩餘）', v: '2,438 次', c: 'var(--violet)' },
          ].map((s, i) => (
            <div key={i} className="panel" style={{ padding: '14px 18px', display: 'flex', justifyContent: 'space-between', alignItems: 'center' }}>
              <span style={{ fontSize: 13.5, color: 'var(--tx-dim)' }}>{s.l}</span>
              <span className="num" style={{ fontSize: 22, color: s.c }}>{s.v}</span>
            </div>
          ))}
        </div>
      </div>

      {/* top earners */}
      <div className="panel">
        <div className="panel-h"><span className="t">里程排行榜</span><span className="tag">跨賽事累積</span></div>
        <table className="adm-tbl">
          <thead><tr><th>排名</th><th>跑者</th><th>跑團</th><th>累積里程</th><th>已獲得轉盤</th><th>已使用</th><th>剩餘</th></tr></thead>
          <tbody>
            {top.map((r, i) => (
              <tr key={i}>
                <td className="num" style={{ color: i === 0 ? 'var(--gold)' : 'var(--tx-dim)', fontSize: 16 }}>{i + 1}</td>
                <td style={{ fontWeight: 600 }}>{r.name}</td>
                <td style={{ color: 'var(--tx-dim)' }}>{r.team}</td>
                <td><span className="mono tone-fug">{r.km} km</span></td>
                <td className="num">{r.spins}</td>
                <td className="num" style={{ color: 'var(--tx-dim)' }}>{r.used}</td>
                <td><span className={'tag ' + (r.spins - r.used > 0 ? 'y' : '')}>{r.spins - r.used}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </div>
  );
}

const ovl = { position: 'absolute', inset: 0, zIndex: 50, background: 'rgba(5,6,9,.7)', backdropFilter: 'blur(4px)', display: 'flex', alignItems: 'center', justifyContent: 'center', padding: 30 };
const titleInput = { background: 'transparent', border: 'none', borderBottom: '1px solid var(--line-2)', color: 'var(--tx)', fontFamily: 'var(--font-disp)', fontWeight: 700, fontSize: 16, padding: '3px 0', width: '100%', marginTop: 2 };

Object.assign(window, { RacesAdmin, SignupsAdmin, MissionsAdmin, StoresAdmin, WheelAdmin, StickersAdmin, StatusTag, FactionsAdmin, TeamsAdmin, NotificationsAdmin, MileageAdmin });
