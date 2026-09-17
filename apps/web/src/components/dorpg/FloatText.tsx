'use client';

// DORPG 飄字（P1）：隊員卡「內部」由中段往上飄一小段顯示傷害／治療／護盾／未命中的瞬時回饋，
// 700ms 後自動消失（2026-09-14 審查修正：原本飄到卡片外會蓋住頂欄的 DORPG 標題，見 .module.css）。
// 消失時機由這裡的 setTimeout 控制，不依賴 CSS animationend——BattleScreen 的「減少動態」設定會對整棵樹
// 套用 animation:none!important（見 BattleScreen.module.css .root[data-reduce-motion]），若消失也靠 CSS
// 動畫結尾觸發，開了減少動態就會卡在畫面上飄字不散；改用 JS 計時器就不受那條全域規則影響。
// 要重播同一句飄字時，呼叫端只需換一個新的 `key`（見 PartyCardProps.floatText.key）讓 React 整個重新掛載本
// 元件，這個 effect 自然重跑，元件本身不必額外偵測 props 是否「看起來一樣」。
import { useEffect, useState } from 'react';
import { PALETTE } from '@/lib/dorpg/assets';
import styles from './FloatText.module.css';

// P5 POLISH：新增 'buff'/'debuff' 兩種語氣（見 engine BattleEvent 'statusApplied'）——跟既有四種
// 共用同一顆計時器/淡出機制，只是換一份色票，不是另立一套元件。
export type FloatTextTone = 'damage' | 'heal' | 'shield' | 'miss' | 'buff' | 'debuff';

export type FloatTextProps = {
  text: string;
  tone: FloatTextTone;
  /** 減少動態：不做位移／淡出動畫，700ms 到即直接消失（OS 層級的 prefers-reduced-motion 已由 CSS 處理，
   * 這個 prop 給呼叫端在需要依「app 內設定」而非系統設定強制切換時使用）。 */
  reducedMotion?: boolean;
  /**
   * P5 POLISH：戰場上的敵人沒有像隊員卡（PartyCard 的 .wrap）那樣「跟著卡片走」的 position:relative
   * 容器可以掛百分比座標——debuff 命中敵人時，呼叫端（BattleScreen.tsx）改傳整個戰場座標系裡的
   * 絕對像素點（來自 BattleStageHandle.getEnemyAnchor，跟 'attack' 事件算特效座標用同一支函式）。
   * 給了這個 prop 就切換成「定點模式」：定位用 left/top 像素＋transform 位移，不吃卡片版面的
   * 35%→5% 百分比動畫（那組數字是配合固定高度的卡片校準的，套在任意高度的戰場上會嚴重跑版）。
   */
  anchorPx?: { x: number; y: number };
};

const DURATION_MS = 700;

/** 六種語氣對應色票；miss 沒有專屬色票，借 textSecondary（灰）。buff/debuff 各借一個目前六色票裡
 *  還沒被其他語氣用過的顏色（mpFill 藍／criticalGlow 亮紅），跟 damage(粉紅)/heal(綠)/shield(金)
 *  視覺上都能分開，不必新增色碼（顏色一律只從 assets.ts 的 PALETTE 來，見檔案慣例）。 */
const TONE_COLOR: Record<FloatTextTone, string> = {
  damage: PALETTE.hpEnemy,
  heal: PALETTE.hpNormal,
  shield: PALETTE.borderGold,
  miss: PALETTE.textSecondary,
  buff: PALETTE.mpFill,
  debuff: PALETTE.criticalGlow,
};

export default function FloatText({ text, tone, reducedMotion = false, anchorPx }: FloatTextProps) {
  const [visible, setVisible] = useState(true);

  useEffect(() => {
    const id = window.setTimeout(() => setVisible(false), DURATION_MS);
    return () => window.clearTimeout(id);
  }, []);

  if (!visible) return null;

  if (anchorPx) {
    // 定點模式（見 FloatTextProps.anchorPx 註解）：位置由呼叫端算好的像素座標決定，動畫走
    // transform（.point 系列 class），不依賴容器高度。
    return (
      <div
        className={reducedMotion ? `${styles.point} ${styles.pointReduced}` : styles.point}
        style={{ left: anchorPx.x, top: anchorPx.y, color: TONE_COLOR[tone] }}
        aria-hidden="true"
      >
        {text}
      </div>
    );
  }

  return (
    <div
      className={reducedMotion ? `${styles.text} ${styles.reduced}` : styles.text}
      style={{ color: TONE_COLOR[tone] }}
      aria-hidden="true"
    >
      {text}
    </div>
  );
}
