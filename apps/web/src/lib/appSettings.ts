// 系統設定型錄：後台「系統設定」頁依此渲染欄位（key 對應後端 app_settings）。
// 之後要新增可調參數，只要在這裡加一列 + 後端 specs 登記對應 key 的驗證即可。
export interface SettingSpec {
  key: string
  group: string
  label: string
  help: string
  type: 'number' | 'select'
  unit?: string
  min?: number
  max?: number
  def: string // 預設值（統一字串化）
  options?: { value: string; label: string }[]
}

export const SETTINGS_SPECS: SettingSpec[] = [
  {
    key: 'active_skin', group: '前台主題（Skin）', label: '前台風格', type: 'select', def: 'default',
    help: '切換前台整體視覺風格。之後可為不同主打主題活動加入對應風格。切換後前台「下次載入」即套用（後台不受影響）。',
    options: [
      { value: 'default', label: '預設（暗黑電影風）' },
      { value: 'warm', label: '溫暖貓狗風（奶油淺色・Pawrathon）' },
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
]
