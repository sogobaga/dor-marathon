// 全螢幕覆蓋層掛載點：在 PhoneShell 手機模擬框(#app-shell)內→portal 進框 + absolute(被框住、桌機不鋪滿視窗)；
// 找不到框(獨立路由如 /track)→退回 document.body + fixed(維持視窗)。SSR(document 未定義)回 node:null/fixed。
export function overlayMount(): { node: HTMLElement | null; position: 'fixed' | 'absolute' } {
  if (typeof document === 'undefined') return { node: null, position: 'fixed' }
  const shell = document.getElementById('app-shell')
  return shell ? { node: shell, position: 'absolute' } : { node: document.body, position: 'fixed' }
}
