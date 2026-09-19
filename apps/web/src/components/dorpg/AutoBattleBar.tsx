'use client';

// DORPG P9（玩家自動戰鬥，見 scratchpad/dorpg_p9/CONTRACT.md §1/§4/§5、WIRE.md §引擎/§戰鬥 bootstrap）：
// 戰鬥 HUD 的「自動」切換鈕＋策略選單。純展示＋回呼元件，不知道 dispatch／PUT /rpg/auto-battle
// 這些細節——BattleScreen.tsx 負責把切換/選策略轉成 SET_AUTO_BATTLE 指令與持久化請求，這裡只管
// 畫面。顏色比照 BattleScreen.tsx 檔頭慣例，只吃 PALETTE 已經寫進根節點的 --c-* 自訂變數，不自己
// 讀 PALETTE／不寫死色碼；不使用 position:fixed（本來就是 BattleScreen 版面裡的一個 band，隨欄捲動）。
import type { CSSProperties } from 'react';
import { strategyLabel, STRATEGY_IDS } from '@/lib/rpgMeta';

export type AutoBattleBarProps = {
  enabled: boolean;
  /** 目前套用的 AI 策略 id（state.party[0].strategyId，即玩家的 auto_strategy_id）。 */
  strategyId: string;
  /**
   * 依 bootstrap config.aiStrategies 篩出的可用策略 id（只含後台 is_active 的六種）；undefined
   * 或空陣列時退回 rpgMeta.STRATEGY_IDS 全部六種（/dev 預覽等尚未接真實 bootstrap 的情境，或
   * ENGINE 尚未把 aiStrategies 併進 BattleConfig 之前的過渡期）。
   */
  availableStrategyIds?: string[];
  /** 正在打 PUT /rpg/auto-battle（純視覺提示，不封鎖互動——本地一律先靠 dispatch 即時生效）。 */
  syncing?: boolean;
  /** 上一次持久化失敗的訊息；BattleScreen 用一個會自動消失的 state 控制，這裡純顯示。 */
  errorText?: string;
  onToggle: (enabled: boolean) => void;
  onSelectStrategy: (strategyId: string) => void;
  /** 邏輯高 px，需與 BattleScreen 幫這個 band 保留的高度一致（見該檔 AUTO_BATTLE_BAR_H）。 */
  height: number;
};

export default function AutoBattleBar({
  enabled, strategyId, availableStrategyIds, syncing, errorText, onToggle, onSelectStrategy, height,
}: AutoBattleBarProps) {
  // 明確標注 string[]：STRATEGY_IDS 是窄字面量聯集的 readonly tuple，跟 availableStrategyIds（string[]）
  // 直接三元運算會讓 TS 把 ids 推成聯集型別，害下面 .includes(strategyId) 要求 strategyId 也符合那個
  // 窄聯集（strategyId 其實是後端可能給出任意字串的一般 string）——這裡用 [...STRATEGY_IDS] 攤平成
  // 一般 string[] 就沒有這個問題。
  const ids: string[] = availableStrategyIds && availableStrategyIds.length > 0 ? availableStrategyIds : [...STRATEGY_IDS];
  // 目前的 strategyId 萬一不在可選清單裡（例如後台把它下架了）也要讓 <select> 有東西可顯示，
  // 補進去當一個獨立選項，而不是讓瀏覽器擅自跳去第一個、悄悄改掉使用者原本選的策略。
  const selectIds = ids.includes(strategyId) ? ids : [strategyId, ...ids];

  return (
    <div style={{ ...root, height }}>
      <button
        type="button"
        onClick={() => onToggle(!enabled)}
        style={{ ...toggleBtn, ...(enabled ? toggleBtnOn : toggleBtnOff) }}
        aria-pressed={enabled}
      >
        {enabled ? `AI・${strategyLabel(strategyId)}` : '自動'}
      </button>
      <select
        value={strategyId}
        onChange={(e) => onSelectStrategy(e.target.value)}
        style={selectStyle}
        aria-label="AI 策略"
      >
        {selectIds.map((id) => (
          <option key={id} value={id}>{strategyLabel(id)}</option>
        ))}
      </select>
      {syncing && <span style={hintStyle}>同步中…</span>}
      {!syncing && errorText && <span style={errStyle}>{errorText}</span>}
    </div>
  );
}

const root: CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, padding: '0 10px', boxSizing: 'border-box', width: '100%',
};
// 金底白字（全站通則）：開啟時金底、文字強制白色；關閉時素色外框鈕，不搶視覺重量。
const toggleBtn: CSSProperties = {
  flexShrink: 0, border: 'none', borderRadius: 999, padding: '5px 12px', fontSize: 12, fontWeight: 800,
  fontFamily: 'inherit', cursor: 'pointer', whiteSpace: 'nowrap',
};
const toggleBtnOn: CSSProperties = { background: 'var(--c-gold)', color: '#fff' };
const toggleBtnOff: CSSProperties = {
  background: 'var(--c-panel)', color: 'var(--c-text)', border: '1px solid var(--c-muted)',
};
const selectStyle: CSSProperties = {
  flex: 1, minWidth: 0, background: 'var(--c-panel)', color: 'var(--c-text)', border: '1px solid var(--c-muted)',
  borderRadius: 8, padding: '5px 8px', fontSize: 11.5, fontFamily: 'inherit',
};
const hintStyle: CSSProperties = { flexShrink: 0, fontSize: 10.5, color: 'var(--c-muted)' };
const errStyle: CSSProperties = { flexShrink: 0, fontSize: 10.5, color: '#f4623a', maxWidth: 120, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' };
