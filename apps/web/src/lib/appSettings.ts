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
  scale?: number // number 型別專用：後端儲存值＝顯示值×scale（例：面額用「元」顯示，換算成「分」存 app_settings）。省略＝1（顯示=儲存，多數欄位如此）
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
    key: 'gallery_entry_state', group: '卡片探索入口', label: '入口顯示狀態', type: 'select', def: 'hidden',
    help: '控制「會員面板下方的卡片探索按鈕」對前台玩家的可見性。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'gallery_entry_whitelist', group: '卡片探索入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'title_entry_state', group: '成就探索（稱號）入口', label: '入口顯示狀態', type: 'select', def: 'whitelist',
    help: '控制「會員面板的 成就探索（稱號系統）按鈕」對前台玩家的可見性。測試中，建議先「僅指定帳號」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'title_entry_whitelist', group: '成就探索（稱號）入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'achievement_entry_state', group: '數據探索入口', label: '入口顯示狀態', type: 'select', def: 'whitelist',
    help: '控制「會員面板的數據探索按鈕」對前台玩家的可見性。測試中，建議先「僅指定帳號」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'achievement_entry_whitelist', group: '數據探索入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'monopoly_entry_state', group: '環台大富翁入口', label: '入口顯示狀態', type: 'select', def: 'whitelist',
    help: '控制「會員面板的環台大富翁按鈕」對前台玩家的可見性。測試中，建議先「僅指定帳號」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'monopoly_entry_whitelist', group: '環台大富翁入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'knowledge_entry_state', group: '知識探索入口', label: '入口顯示狀態', type: 'select', def: 'hidden',
    help: '控制「會員面板的知識探索（知識卡圖鑑）按鈕」對前台玩家的可見性。測試中，建議先「僅指定帳號」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'knowledge_entry_whitelist', group: '知識探索入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'training_entry_state', group: '自主訓練入口', label: '入口顯示狀態', type: 'select', def: 'whitelist',
    help: '控制「會員面板的自主訓練按鈕」對前台玩家的可見性（VIP 限定功能）。測試中，建議先「僅指定帳號」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'training_entry_whitelist', group: '自主訓練入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'strategy_entry_state', group: '賽事策略入口', label: '入口顯示狀態', type: 'select', def: 'whitelist',
    help: '控制「自主訓練頁的🏁賽事策略分頁」對前台玩家的可見性。測試中，建議先「僅指定帳號」。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'strategy_entry_whitelist', group: '賽事策略入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'cheer_test_entry_state', group: '跑步應援', label: '應援測試按鈕入口', type: 'select', def: 'whitelist',
    help: 'GPS 跑步頁的「測試應援」按鈕；預設僅白名單帳號可見。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'cheer_test_entry_whitelist', group: '跑步應援', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'cheer_display_ms', group: '跑步應援', label: '應援表演顯示時間（毫秒）', type: 'number', unit: 'ms',
    help: '每公里應援的泡泡框＋啦啦隊出現後停留多久再消失；預設 3000 = 3 秒。',
    min: 500, max: 60000, def: '3000',
  },
  {
    key: 'cheer_edit_entry_state', group: '跑步應援', label: '啦啦隊位置校正模式入口', type: 'select', def: 'whitelist',
    help: 'GPS 跑步頁的『🎯 校正啦啦隊』按鈕與 ?cheerEdit=1 連結；校正完成後可改為隱藏關閉。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'cheer_edit_entry_whitelist', group: '跑步應援', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'cheer_char_layout', group: '跑步應援', label: '啦啦隊位置校正值（JSON）', type: 'text', def: '',
    help: '由前台校正模式儲存；一般不需手動編輯，清空或貼回預設即可重設。',
    rows: 3,
  },
  {
    key: 'gps_calib_entry_state', group: 'GPS 校正', label: 'GPS 距離校正入口', type: 'select', def: 'whitelist',
    help: '以連接的手錶/App(Strava/Garmin/COROS)紀錄為參考，自動估計並校正 App GPS 距離的系統性偏差（見個人資料頁「GPS 距離校正」卡片、GPS 上傳當下即套用）。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不套用（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號套用（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'gps_calib_entry_whitelist', group: 'GPS 校正', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號套用」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'gps_calib_notify_whitelist', group: 'GPS 校正', label: '校正通知信白名單', type: 'text', def: '',
    help: '⚠️ 空白或未設定＝一封都不發（不是「全部都發」）。只有名單內的帳號會收到「GPS 距離校正已啟用／暫停中」站內信，'
      + '與上方「是否套用校正」的白名單完全分開——就算入口改成「全部開放」，也只有這裡列出的帳號會收到信。'
      + '一行一個，可填帳號編碼（#可省）或註冊 Email。（種子值由 migration 155 寫入；若這裡顯示「未設定」代表該 migration 尚未套用。）',
    placeholder: 'sogobaga@gmail.com', rows: 3,
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
  {
    key: 'vip_coupon_value_cents', group: 'VIP 訂閱制', label: '活動優惠券面額', type: 'number', unit: '元', scale: 100,
    help: '每張活動優惠券可折抵的金額（僅能用於賽事報名費折抵，與優惠序號擇一、報名費 0 元時不可用）。改動後「立即」套用於之後的報名折抵；玩家已持有的券張數不受影響。',
    min: 1, max: 100000, def: '100',
  },
  {
    key: 'vip_coupon_per_month', group: 'VIP 訂閱制', label: '每月補發張數', type: 'number', unit: '張',
    help: 'VIP 會員每個月自動補發到幾張活動優惠券（lazy 補券：登入或報名當下若偵測到本月尚未補過才補齊，不會超額累加）。',
    min: 0, max: 100, def: '3',
  },
  {
    key: 'explore_checkin_daily_cap_normal', group: '城市探索・打卡', label: '每日打卡上限（一般會員）', type: 'number', unit: '次',
    help: '一般會員每日可打卡次數上限（跨所有打卡點加總，以台北時區計日）。',
    min: 1, max: 50, def: '3',
  },
  {
    key: 'explore_checkin_daily_cap_vip', group: '城市探索・打卡', label: '每日打卡上限（VIP）', type: 'number', unit: '次',
    help: 'VIP 會員每日可打卡次數上限（跨所有打卡點加總，以台北時區計日）。',
    min: 1, max: 50, def: '5',
  },
  {
    key: 'explore_checkin_cooldown_hours', group: '城市探索・打卡', label: '同點打卡冷卻', type: 'number', unit: '小時',
    help: '同一個打卡點打卡成功後，需等待多久才能對「同一點」再次打卡拿獎勵。',
    min: 1, max: 720, def: '24',
  },
  {
    key: 'explore_checkin_dp_min', group: '城市探索・打卡', label: '打卡 DP 下限', type: 'number', unit: 'DP',
    help: '每次打卡成功隨機發放的 DP 下限（系統級預設）。打卡點若在「城市探索關主編輯」個別設了非 0 的打卡 DP 區間，該點優先用自己的設定，不吃這裡。',
    min: 0, max: 9999, def: '1',
  },
  {
    key: 'explore_checkin_dp_max', group: '城市探索・打卡', label: '打卡 DP 上限', type: 'number', unit: 'DP',
    help: '每次打卡成功隨機發放的 DP 上限（系統級預設）。打卡點若個別設了非 0 的打卡 DP 區間，該點優先用自己的設定。',
    min: 0, max: 9999, def: '3',
  },
  {
    key: 'explore_checkin_gp_min', group: '城市探索・打卡', label: '打卡 GP 下限', type: 'number', unit: 'GP',
    help: '每次打卡成功隨機發放的 GP 下限（系統級預設）。打卡點若個別設了非 0 的打卡 GP 區間，該點優先用自己的設定。',
    min: 0, max: 9999, def: '1',
  },
  {
    key: 'explore_checkin_gp_max', group: '城市探索・打卡', label: '打卡 GP 上限', type: 'number', unit: 'GP',
    help: '每次打卡成功隨機發放的 GP 上限（系統級預設）。打卡點若個別設了非 0 的打卡 GP 區間，該點優先用自己的設定。',
    min: 0, max: 9999, def: '2',
  },
  {
    key: 'explore_complete_gp_min', group: '城市探索・打卡', label: '關主完成 GP 下限', type: 'number', unit: 'GP',
    help: '完成關主挑戰時，依機率額外發放的 GP 下限（系統級預設）。打卡點若個別設了非 0 的完成 GP 上限或機率，該點優先用自己的設定（三個值一起）。',
    min: 0, max: 9999, def: '5',
  },
  {
    key: 'explore_complete_gp_max', group: '城市探索・打卡', label: '關主完成 GP 上限', type: 'number', unit: 'GP',
    help: '完成關主挑戰時，依機率額外發放的 GP 上限（系統級預設）。打卡點若個別設了非 0 的完成 GP 上限或機率，該點優先用自己的設定（三個值一起）。',
    min: 0, max: 9999, def: '10',
  },
  {
    key: 'explore_complete_gp_chance', group: '城市探索・打卡', label: '關主完成給 GP 機率(%)', type: 'number', unit: '%',
    help: '完成關主挑戰時額外發放 GP 的機率（系統級預設，0=不發）。打卡點若個別設了非 0 的完成 GP 上限或機率，該點優先用自己的設定（三個值一起）。',
    min: 0, max: 100, def: '30',
  },
  // ── 團練邀請（見 services/api/internal/runmeet，migration 156）──
  // ⚠️ 中文顯示一律「團練」，不得寫成「跑團」（賽事已有「跑團分組」，撞名會混淆）。
  // ⚠️ 白名單那列務必 type:'text'——admin/system/page.tsx 只有 type==='number' 才做 Number(raw)/scale，
  //    text 型誤設成 number 會被寫成字串 "NaN"（正式 DB 的 strategy_entry_whitelist 真的被毀過一次）。
  {
    key: 'runmeet_entry_state', group: '團練邀請入口', label: '入口顯示狀態', type: 'select', def: 'whitelist',
    help: '控制「會員面板的團練邀請按鈕」對前台玩家的可見性。測試中，建議先「僅指定帳號」。後端同一份設定也會擋 API（非白名單直接 403），不是只藏 UI。',
    options: [
      { value: 'hidden', label: '前台隱藏（都看不到）' },
      { value: 'locked', label: '顯示但不能按（即將開放）' },
      { value: 'whitelist', label: '顯示且指定帳號可按（下方白名單）' },
      { value: 'open', label: '顯示且全部開放（正式開放）' },
    ],
  },
  {
    key: 'runmeet_entry_whitelist', group: '團練邀請入口', label: '指定帳號白名單', type: 'text', def: '',
    help: '僅在上方選「指定帳號可按」時生效。一行一個，可填帳號編碼（#可省）或註冊 Email。',
    placeholder: '#8U2TGUWE\nsomeone@example.com', rows: 4,
  },
  {
    key: 'runmeet_create_requires_vip', group: '團練邀請規則', label: '發起團練限 VIP', type: 'select', def: '0',
    help: '預設關閉：一般會員也能發起（每月次數較少），這是最強的 VIP 轉換鉤子。開啟後，非 VIP 按「＋ 發起」會直接跳 VIP 引導彈窗，且不消耗任何次數。加入團練一律不限 VIP。',
    options: [
      { value: '0', label: '關閉（一般會員也能發起）' },
      { value: '1', label: '開啟（只有 VIP 能發起）' },
    ],
  },
  {
    key: 'runmeet_quota_normal', group: '團練邀請規則', label: '一般會員每月發起次數', type: 'number', unit: '次',
    help: '一般會員每個自然月（台北時間，每月 1 日 00:00 重置）可發起幾個團練。⚠️ 建立後即使關閉或刪除，次數一律不返還——唯一的返還管道是後台「人工調整配額」。',
    min: 1, max: 50, def: '1',
  },
  {
    key: 'runmeet_quota_vip', group: '團練邀請規則', label: 'VIP 每月發起次數', type: 'number', unit: '次',
    help: 'VIP 每個自然月可發起幾個團練。上限在「每次建立當下」依 VIP 狀態即時計算，所以月中升級 VIP 的人立刻享有這個額度（已用掉的次數仍照算）。',
    min: 1, max: 50, def: '10',
  },
  {
    key: 'runmeet_images_normal', group: '團練邀請規則', label: '一般會員每團圖片張數', type: 'number', unit: '張',
    help: '一般會員每個團練可上傳幾張圖片。⚠️ 這個值是「建立當下的快照」，寫進該團練——之後改設定不影響已建立的團練（避免 VIP 到期後連改人數上限都被擋死）。',
    min: 1, max: 4, def: '1',
  },
  {
    key: 'runmeet_images_vip', group: '團練邀請規則', label: 'VIP 每團圖片張數', type: 'number', unit: '張',
    help: 'VIP 每個團練可上傳幾張圖片（同樣是建立當下的快照）。圖片只收 JPG／PNG，上傳後一律重新編碼（去除 EXIF 含 GPS 座標與夾帶內容）。',
    min: 1, max: 4, def: '4',
  },
  {
    key: 'runmeet_capacity_max', group: '團練邀請規則', label: '人數上限的上限', type: 'number', unit: '人',
    help: '發起人設定「這個團練最多幾人」時可填的最大值（含發起人本人）。最小恆為 2 人。',
    min: 2, max: 500, def: '50',
  },
  {
    key: 'runmeet_pending_max', group: '團練邀請規則', label: '待審核申請上限', type: 'number', unit: '筆',
    help: '審核制團練最多可同時累積幾筆待審核申請，超過就擋下新申請（避免發起人信箱被灌爆）。待審核不占名額。',
    min: 1, max: 500, def: '50',
  },
  {
    key: 'runmeet_comment_daily_cap', group: '團練邀請規則', label: '每人每日留言上限', type: 'number', unit: '則',
    help: '同一位會員每天（台北時間）在所有團練加總最多可發幾則留言。另有「兩則間隔 3 秒」與「10 分鐘內同一團不得重複相同內容」兩道節流。',
    min: 1, max: 1000, def: '100',
  },
  {
    key: 'runmeet_reject_cooldown_hours', group: '團練邀請規則', label: '婉拒後冷卻時數', type: 'number', unit: '小時',
    help: '申請被發起人婉拒後，要等幾小時才能對同一個團練再次申請（0＝不冷卻）。被「剔除」則是永久不能再加入，需發起人在成員管理手動解除。',
    min: 0, max: 720, def: '24',
  },
  {
    key: 'runmeet_ended_visible_days', group: '團練邀請規則', label: '已結束保留天數', type: 'number', unit: '天',
    help: '團練時間過了之後，還要在探索頁底部的「已結束」折疊區顯示幾天。超過就從探索消失，但成員仍可在「我的團練」看到，資料不刪除。',
    min: 1, max: 365, def: '90',
  },
]
