// 團練同步跑（Group Run Live）共用型別——契約 docs/runmeet/GROUP_RUN_LIVE_CONTRACT.md §6。
// P2（useMeetLive／Leaflet／專注模式）與 P3（三套 skin 渲染）共用；純型別、無 runtime 程式碼，
// 讓三套 skin 的 types.ts 可以安全 `import type`，不拖進任何 bundle 內容。

export type PeerDot = { n: number; name: string; lat: number; lng: number; acc: number; rxAt: number /*client ms*/; ageS: number /*server age at receipt*/ }
export type StaleThresholds = { fadeS: number; grayS: number; dropS: number }
export type MeetLivePeers = { active: boolean; dots: PeerDot[]; stale: StaleThresholds; selfRing: boolean }
export type MeetLiveState = 'idle'|'starting'|'live'|'need_fix'|'presence_only'|'paused'|'reconnecting'|'stopped'|'error'
export type MeetLiveStats = { state: MeetLiveState; live: number; iv: number; sharing: boolean; presenceOnly: boolean; message?: string; messageAt?: number; nearest: { n: number; name: string; distM: number; bearingDeg: number }[] }
