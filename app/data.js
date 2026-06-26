/* DOR mock data — exposed on window.DOR */
window.DOR = (function () {
  const races = [
    {
      id: 'hunt2026',
      title: '獵人之夜',
      sub: 'HUNTERS NIGHT',
      world: '獵人 vs 逃亡者',
      status: 'live',            // live | open | soon | done
      distances: [10, 21, 42],
      myDistance: 21,
      days: 7, dayNow: 3,
      start: '06/12', end: '06/18',
      teams: 1284, mine: '台北夜行者',
      faction: 'fugitive',        // my faction in this race
      groupType: 'faction',       // distance | faction | club
      groupMode: 'random',        // self | random
      factions: [
        { id: 'fugitive', name: '逃亡者', color: 'fug' },
        { id: 'hunter',   name: '獵人',   color: 'hunt' },
      ],
      hero: '主視覺 ｜ 城市夜景 + 霓虹追逐',
      blurb: '一半的人追，一半的人逃。對抗只在虛擬戰局，不在真實世界。',
      progressKm: 12.4,
      goalKm: 21,
    },
    {
      id: 'relay2026',
      title: '城市公會戰',
      sub: 'GUILD RELAY',
      world: '跑團 vs 跑團',
      status: 'open',
      distances: [10, 21, 42],
      days: 14, dayNow: 0,
      start: '07/01', end: '07/14',
      teams: 642, mine: null,
      faction: null,
      groupType: 'club',
      groupMode: 'self',
      clubs: [
        { id: 'c1', name: '台北夜行者' },
        { id: 'c2', name: '高雄海岸騎士' },
        { id: 'c3', name: '台中節拍跑者' },
        { id: 'c4', name: '新竹晨曦跑者' },
      ],
      hero: '主視覺 ｜ 城市地圖 + 公會旗幟',
      blurb: '跑團不只是團報，而是代表自己的公會出征。比完成率、比救援、比逆轉。',
      progressKm: 0, goalKm: 0,
    },
    {
      id: 'signal2026',
      title: '穩定訊號',
      sub: 'STEADY SIGNAL',
      world: '配速控制挑戰',
      status: 'soon',
      distances: [10, 21],
      days: 5, dayNow: 0,
      start: '07/20', end: '07/24',
      teams: 0, mine: null,
      faction: null,
      groupType: 'distance',
      groupMode: 'self',
      hero: '主視覺 ｜ 心率波形 + 城市晨光',
      blurb: '慢不是弱，是潛伏。把配速波動控制在區間內，維持訊號不被獵人偵測。',
      progressKm: 0, goalKm: 0,
    },
    {
      id: 'spring2026',
      title: '春季逃亡賽',
      sub: 'SPRING ESCAPE',
      world: '獵人 vs 逃亡者',
      status: 'done',
      distances: [21, 42],
      myDistance: 42,
      days: 7, dayNow: 7,
      start: '03/03', end: '03/09',
      teams: 980, mine: '台北夜行者',
      faction: 'fugitive',
      groupType: 'faction',
      groupMode: 'random',
      factions: [
        { id: 'fugitive', name: '逃亡者', color: 'fug' },
        { id: 'hunter',   name: '獵人',   color: 'hunt' },
      ],
      hero: '主視覺 ｜ 春日街景',
      blurb: '',
      progressKm: 42, goalKm: 42,
    },
  ];

  // 7-day mission cards for the live race
  const missions = [
    { day: 1, title: '身份啟動', tag: 'IDENTITY', type: 'base', icon: 'id',
      base: 2.0, paceLo: null, paceHi: null, done: true, kmDone: 2.4, rescued: 0,
      desc: '揭曉你的陣營身份。完成 2K 啟動跑，啟用今日身份卡。' },
    { day: 2, title: '低速潛伏', tag: 'STEALTH', type: 'pace', icon: 'stealth',
      base: 3.0, paceLo: '9:30', paceHi: '10:30', done: true, kmDone: 3.2, rescued: 0,
      desc: '以 9:30–10:30 配速完成 3K，維持低訊號避免被獵人偵測。' },
    { day: 3, title: '高速追擊', tag: 'CHASE', type: 'pace', icon: 'chase',
      base: 3.0, paceLo: '4:30', paceHi: '5:30', done: false, kmDone: 1.8, rescued: 1,
      desc: '以 4:30–5:30 配速完成 3K 追擊。完成基礎里程後，每多跑 1K 可多拯救 1 位夥伴。' },
    { day: 4, title: '誘餌與搜索', tag: 'DECOY', type: 'base', icon: 'decoy',
      base: 4.0, paceLo: null, paceHi: null, done: false, kmDone: 0, rescued: 0,
      desc: '完成 4K 佈設誘餌，擾亂獵人搜索路線。' },
    { day: 5, title: '救援日', tag: 'RESCUE', type: 'rescue', icon: 'rescue',
      base: 3.0, paceLo: null, paceHi: null, done: false, kmDone: 0, rescued: 0,
      desc: '完成 3K 基礎里程後，每多完成 1K 釋放 1 位被捕的夥伴。今日救援值加倍。' },
    { day: 6, title: '壓縮戰局', tag: 'SQUEEZE', type: 'pace', icon: 'squeeze',
      base: 5.0, paceLo: '5:30', paceHi: '6:30', done: false, kmDone: 0, rescued: 0,
      desc: '以 5:30–6:30 配速完成 5K，壓縮獵人包圍圈。' },
    { day: 7, title: '最終追逐', tag: 'FINALE', type: 'base', icon: 'finale',
      base: 6.0, paceLo: null, paceHi: null, done: false, kmDone: 0, rescued: 0,
      desc: '最終結算日。完成 6K 決定戰局翻盤與否。' },
  ];

  const factionBoard = {
    fugitive: { label: '逃亡者', val: 58, escaped: 742, color: 'fug' },
    hunter:   { label: '獵人',   val: 42, captured: 531, color: 'hunt' },
  };

  // check-in stores (門市)
  const stores = [
    { id: 's1', name: 'DOR 補給站・信義門市', addr: '台北市信義區松壽路 12 號', dist: 0.4,
      hours: '24 小時', stamp: true,  task: '抵達門市出示身份卡，領取今日補給包', city: '台北' },
    { id: 's2', name: 'DOR 補給站・大安門市', addr: '台北市大安區敦化南路一段 187 號', dist: 1.2,
      hours: '06:00 – 24:00', stamp: false, task: '完成 3K 後於門市打卡，解鎖支線任務', city: '台北' },
    { id: 's3', name: 'DOR 補給站・中山門市', addr: '台北市中山區南京東路二段 45 號', dist: 2.8,
      hours: '24 小時', stamp: false, task: '夜間打卡點，獵人偵測範圍外', city: '台北' },
    { id: 's4', name: 'DOR 補給站・板橋門市', addr: '新北市板橋區文化路一段 268 號', dist: 5.1,
      hours: '06:00 – 24:00', stamp: false, task: '跨區打卡，額外 +50 救援值', city: '新北' },
  ];

  // wheel reward pool (random) — weights sum need not be 100
  const wheel = [
    { id: 'lp50',  kind: 'line',    label: 'LINE Points', amount: 50,  weight: 26, color: 'gold' },
    { id: 'lp100', kind: 'line',    label: 'LINE Points', amount: 100, weight: 14, color: 'gold' },
    { id: 'lp300', kind: 'line',    label: 'LINE Points', amount: 300, weight: 4,  color: 'gold' },
    { id: 'card',  kind: 'sticker', label: '九宮格集點卡', amount: 1,   weight: 30, color: 'violet' },
    { id: 'card2', kind: 'sticker', label: '集點卡 ×2',    amount: 2,   weight: 8,  color: 'violet' },
    { id: 'again', kind: 'again',   label: '再轉一次',      amount: 0,   weight: 10, color: 'fug' },
    { id: 'miss',  kind: 'miss',    label: '銘謝惠顧',      amount: 0,   weight: 8,  color: 'dim' },
  ];

  // 九宮格集點卡 — 9 cards,集滿 → 完賽公仔
  const stickers = [
    { i: 1, name: '逃亡者', got: true },
    { i: 2, name: '獵人',   got: true },
    { i: 3, name: '誘餌',   got: true },
    { i: 4, name: '訊號',   got: true },
    { i: 5, name: '救援',   got: false },
    { i: 6, name: '潛伏',   got: true },
    { i: 7, name: '追擊',   got: false },
    { i: 8, name: '戰報',   got: false },
    { i: 9, name: '公仔',   got: false },
  ];

  const records = [
    { id:'spring2026', title:'春季逃亡賽', dist:42, time:'4:58:12', date:'2026.03.09', faction:'fugitive', rank:'逆轉者', medal:'#FFC24B' },
    { id:'winter2025', title:'冬季獵殺賽', dist:21, time:'2:12:40', date:'2025.12.21', faction:'hunter',   rank:'捕獲王', medal:'#FF4B5C' },
    { id:'auto2025',   title:'秋季潛行賽', dist:21, time:'2:31:05', date:'2025.10.12', faction:'fugitive', rank:'潛伏者', medal:'#2DE59A' },
    { id:'sum2025',    title:'夏季訊號賽', dist:10, time:'1:02:18', date:'2025.07.30', faction:'fugitive', rank:'穩定訊號',medal:'#9D8CFF' },
  ];

  const profile = {
    name: '陳逸帆',
    handle: '@yifan.runs',
    faction: 'fugitive',
    factionLabel: '逃亡者',
    team: '台北夜行者',
    avatar: '跑者頭像',
    totalKm: 94,            // 累積完賽里程 (42+21+21+10)
    spinsUsed: 0,
    races: 4,
    rescues: 17,
    streak: 12,
  };

  return { races, missions, factionBoard, stores, wheel, stickers, records, profile };
})();
