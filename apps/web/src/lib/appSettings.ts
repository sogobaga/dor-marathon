// 系統設定型錄：後台「系統設定」頁依此渲染欄位（key 對應後端 app_settings）。
// 之後要新增可調參數，只要在這裡加一列 + 後端讀取端用對應 key 即可。
export interface SettingSpec {
  key: string
  group: string
  label: string
  unit: string
  help: string
  min: number
  max: number
  def: number // 預設值（未設定時後端採用的值）
}

export const SETTINGS_SPECS: SettingSpec[] = [
  {
    key: 'event_wait_min_sec', group: '事件任務節奏', label: '事件等待時間（最短）', unit: '秒',
    help: '每次事件之間的隨機等待「下限」。開始跑步、以及每次事件結束後，系統會在 [最短, 最長] 之間隨機取一個等待時間，等到時間到、且符合觸發條件時才會出現下一個事件。也是伺服器端防濫用地板。',
    min: 60, max: 3600, def: 300,
  },
  {
    key: 'event_wait_max_sec', group: '事件任務節奏', label: '事件等待時間（最長）', unit: '秒',
    help: '隨機等待「上限」。與下限一起決定事件出現的隨機節奏（例：300–900 秒＝約 5–15 分鐘一次）。',
    min: 60, max: 3600, def: 900,
  },
]
