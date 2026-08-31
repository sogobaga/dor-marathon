// 全站 data_updated 推播失效：待更新 topics 集合 + 依對應表精準失效 SWR keys。
// SiteRealtime.tsx（WS onmessage）呼叫 addTopic 後以去抖動靜默呼叫 refreshAndClear（v0.1.600 起無 RefreshBadge）。
import { create } from 'zustand'
import { mutate } from 'swr'

export type DataTopic = 'races' | 'dashboard' | 'personal_tasks' | 'explore' | 'settings' | 'runmeet'
export const DATA_TOPICS: readonly DataTopic[] = ['races', 'dashboard', 'personal_tasks', 'explore', 'settings', 'runmeet']

const RACES_KEYS = ['races', 'detail', 'brochure', 'standings', 'leaderboard', 'progress', 'cert', 'exp-bd', 'contrib', 'rangedetail']
const EXPLORE_KEYS = ['explore-gallery', 'explore-list', 'progress']
// 團練邀請（見 lib/api.ts runMeetApi）：後端在成員/狀態/留言變動時 PublishData(ctx,"runmeet",…)。
// ⚠️ topic 沒列進 DATA_TOPICS 會被 SiteRealtime.tsx 靜默丟棄（它直接 import 這裡的常數），
//    這裡與 matcher 兩處都要有，缺一不可。
const RUNMEET_KEYS = ['run-meets', 'run-meet', 'run-meet-mine', 'run-meet-quota', 'run-meet-members', 'run-meet-comments']

// topic → 是否命中某 SWR key 的判斷式（照編排者的精準失效對應表）
const TOPIC_MATCHERS: Record<DataTopic, (key: unknown) => boolean> = {
  races: (key) => Array.isArray(key) && RACES_KEYS.includes(key[0]),
  dashboard: (key) => Array.isArray(key) && key[0] === 'dashboard',
  personal_tasks: (key) => Array.isArray(key) && key[0] === 'personal-plans',
  explore: (key) => Array.isArray(key) && EXPLORE_KEYS.includes(key[0]),
  settings: (key) => key === 'site-settings', // 字串 key，非陣列
  runmeet: (key) => Array.isArray(key) && RUNMEET_KEYS.includes(key[0] as string),
}

interface SiteRealtimeState {
  pendingTopics: Set<DataTopic>
  addTopic: (topic: DataTopic) => void
  // 站內信到達計數：收到 topic=mail 就 +1；MailPanel 訂閱此值→自動重抓未讀數（不經 refresh badge、紅點立即更新）
  mailTick: number
  bumpMail: () => void
  // 對每個待更新 topic 失效對應 SWR keys，然後清空集合。由 SiteRealtime 去抖動後靜默呼叫（背景 revalidate）。
  refreshAndClear: () => void
}

export const useSiteRealtimeStore = create<SiteRealtimeState>((set, get) => ({
  pendingTopics: new Set<DataTopic>(),
  mailTick: 0,
  bumpMail: () => set((s) => ({ mailTick: s.mailTick + 1 })),
  addTopic: (topic) =>
    set((s) => {
      if (s.pendingTopics.has(topic)) return s // 已在集合中，避免不必要的重渲染
      const next = new Set(s.pendingTopics)
      next.add(topic)
      return { pendingTopics: next }
    }),
  refreshAndClear: () => {
    const topics = get().pendingTopics
    topics.forEach((t) => mutate(TOPIC_MATCHERS[t]))
    set({ pendingTopics: new Set<DataTopic>() })
  },
}))
