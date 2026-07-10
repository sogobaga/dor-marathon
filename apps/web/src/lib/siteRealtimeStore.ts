// 全站 data_updated 推播失效：待更新 topics 集合 + 依對應表精準失效 SWR keys。
// SiteRealtime.tsx（WS onmessage）呼叫 addTopic；RefreshBadge.tsx 顯示 + 點擊呼叫 refreshAndClear。
import { create } from 'zustand'
import { mutate } from 'swr'

export type DataTopic = 'races' | 'dashboard' | 'personal_tasks' | 'explore' | 'settings'
export const DATA_TOPICS: readonly DataTopic[] = ['races', 'dashboard', 'personal_tasks', 'explore', 'settings']

const RACES_KEYS = ['races', 'detail', 'brochure', 'standings', 'leaderboard', 'progress', 'cert', 'exp-bd', 'contrib', 'rangedetail']
const EXPLORE_KEYS = ['explore-gallery', 'explore-list', 'progress']

// topic → 是否命中某 SWR key 的判斷式（照編排者的精準失效對應表）
const TOPIC_MATCHERS: Record<DataTopic, (key: unknown) => boolean> = {
  races: (key) => Array.isArray(key) && RACES_KEYS.includes(key[0]),
  dashboard: (key) => Array.isArray(key) && key[0] === 'dashboard',
  personal_tasks: (key) => Array.isArray(key) && key[0] === 'personal-plans',
  explore: (key) => Array.isArray(key) && EXPLORE_KEYS.includes(key[0]),
  settings: (key) => key === 'site-settings', // 字串 key，非陣列
}

interface SiteRealtimeState {
  pendingTopics: Set<DataTopic>
  addTopic: (topic: DataTopic) => void
  // 對每個待更新 topic 失效對應 SWR keys，然後清空集合。絕不自動呼叫——只能由使用者點擊 Badge 觸發。
  refreshAndClear: () => void
}

export const useSiteRealtimeStore = create<SiteRealtimeState>((set, get) => ({
  pendingTopics: new Set<DataTopic>(),
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
