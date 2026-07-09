// 系統設定型錄：後台「系統設定」頁依此渲染欄位（key 對應後端 app_settings）。
// 之後要新增可調參數，只要在這裡加一列 + 後端 specs 登記對應 key 的驗證即可。
export interface SettingSpec {
  key: string
  group: string
  label: string
  help: string
  type: 'number' | 'select' | 'text'
  unit?: string
  min?: number
  max?: number
  def: string // 預設值（統一字串化）
  options?: { value: string; label: string }[]
  placeholder?: string // type='text' 多行輸入框的提示
  rows?: number        // type='text' 多行輸入框行數
}

export const SETTINGS_SPECS: SettingSpec[] = [
  {
    key: 'active_skin', group: '前台主題（Skin）', label: '前台風格', type: 'select', def: 'default',
    help: '切換前台整體視覺風格。之後可為不同主打主題活動加入對應風格。切換後前台「下次載入」即套用（後台不受影響）。',
    options: [
      { value: 'default', label: '預設（暗黑電影風）' },
      { value: 'warm', label: '溫暖貓狗風（奶油淺色・Pawrathon）' },
      { value: 'warm2', label: '城市探索・精緻版（溫暖 2.0）' },
    ],
  },
  {
    key: 'event_wait_min_sec', group: '事件任務節奏', label: '事件等待時間（最短）', type: 'number', unit: '秒',
    help: '每次事件之間的隨機等待「下限」。開始跑步、以及每次事件結束後，系統會在 [最短, 最長] 之間隨機取一個等待時間，等到時間到、且符合觸發條件時才會出現下一個事件。也是伺服器端防濫用地板。',
    min: 60, max: 3600, def: '300',
  },
  {
    key: 'event_wait_max_sec', group: '事件任務節奏', label: '事件等待時間（最長）', type: 'number', unit: '秒',
    help: '隨機等待「上限」。與下限一起決定事件出現的隨機節奏（例：300–900 秒＝約 5–15 分鐘一次）。',
    min: 60, max: 3600, def: '900',
  },
  {
    key: 'event_first_wait_run1_sec', group: '事件任務節奏', label: '新手加速・第 1 趟等待', type: 'number', unit: '秒',
    help: '玩家「第 1 趟」跑步時，第一個事件的等待時間（比正常短很多，讓新玩家一開始就遇得到事件）。依帳號的已完成跑步筆數判定。',
    min: 5, max: 3600, def: '45',
  },
  {
    key: 'event_first_wait_run2_sec', group: '事件任務節奏', label: '新手加速・第 2 趟等待', type: 'number', unit: '秒',
    help: '玩家「第 2 趟」跑步時，第一個事件的等待時間。',
    min: 5, max: 3600, def: '90',
  },
  {
    key: 'event_first_wait_run3_sec', group: '事件任務節奏', label: '新手加速・第 3 趟等待', type: 'number', unit: '秒',
    help: '玩家「第 3 趟」跑步時，第一個事件的等待時間。第 4 趟起回到正常的隨機區間。',
    min: 5, max: 3600, def: '180',
  },
  {
    key: 'personal_entry_state', group: '個人任務入口', label: '入口顯示狀態', type: 'select', def: 'hidden',
    help: '控制「會員面板上的個人任務按鈕」對前台玩家的可見性。初期建議先「隱藏」或「僅指定帳號」，內容備妥後再「全部開放」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'personal_entry_whitelist', group: '個人任務入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email，大小寫不拘。名單外的玩家看不到入口。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'explore_entry_state', group: '城市探索入口', label: '入口顯示狀態', type: 'select', def: 'hidden',
    help: '控制「會員面板下方的城市探索按鈕」對前台玩家的可見性。初期建議「隱藏」或「僅指定帳號」，內容備妥後再「全部開放」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'explore_entry_whitelist', group: '城市探索入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'gallery_entry_state', group: '卡片圖鑑入口', label: '入口顯示狀態', type: 'select', def: 'hidden',
    help: '控制「會員面板下方的卡片圖鑑按鈕」對前台玩家的可見性。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'gallery_entry_whitelist', group: '卡片圖鑑入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'vip_trial_days', group: 'VIP 訂閱制', label: '新註冊試用天數', type: 'number', unit: '天',
    help: '玩家「新註冊完成」後自動獲得 VIP 的天數。試用到期後首次開啟 App 會跳一次「是否續訂」彈窗，之後降為一般會員（VIP 限定功能重新上鎖）。',
    min: 0, max: 365, def: '14',
  },
  {
    key: 'vip_price_monthly', group: 'VIP 訂閱制', label: '月繳原價', type: 'number', unit: '元',
    help: '月繳方案的每月原價（未折扣）。',
    min: 0, max: 100000, def: '399',
  },
  {
    key: 'vip_price_annual', group: 'VIP 訂閱制', label: '年繳原價', type: 'number', unit: '元',
    help: '年繳方案的每年原價（未折扣）。',
    min: 0, max: 1000000, def: '4788',
  },
  {
    key: 'vip_first_promo_monthly_pct', group: 'VIP 訂閱制', label: '首購促銷・月繳實付%', type: 'number', unit: '%',
    help: '試用到期後 14 天內續訂的「月繳」實付百分比（70 = 付七成、即打七折）。原價 399 × 70% = 279。',
    min: 1, max: 100, def: '70',
  },
  {
    key: 'vip_first_promo_annual_pct', group: 'VIP 訂閱制', label: '首購促銷・年繳實付%', type: 'number', unit: '%',
    help: '訂閱「年度方案」的實付百分比（55 = 付五五、即打 5.5 折）。原價 4788 × 55% = 2633。',
    min: 1, max: 100, def: '55',
  },
  {
    key: 'vip_first_promo_days', group: 'VIP 訂閱制', label: '首購促銷窗天數', type: 'number', unit: '天',
    help: '試用到期後幾天內續訂可享上面的首購促銷價；超過此天數即恢復原價（「14 天後恢復原價」）。之後可另在促銷檔期設定其他優惠。',
    min: 0, max: 365, def: '14',
  },
]
