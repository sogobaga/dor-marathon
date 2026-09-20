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
// DORPG P7（武器系統，見契約 dorpg_p7 CONTRACT.md §3/WIRE §引擎：「splash: true 旗標供浮字區分」）：
// 新增 'splash' 語氣——斧的濺射傷害打在同排相鄰怪物身上，不是這次攻擊真正鎖定的目標，用比較不
// 顯眼的樣式（見下方 `small` prop）跟主目標的傷害數字區分，避免玩家誤以為濺射到的怪物也被完整
// 打了一次。色票沿用 'miss' 的灰（textSecondary）——語意上都是「次要/非主要」的訊息。
// DORPG P10（CONTRACT §5：「挑釁浮字『挑釁！』（金色語氣）」）：新增 'taunt' 語氣，借用既有
// PALETTE.targetGold（本檔所在的 dorpg/assets.ts 不在本輪 FRONTEND 寫入範圍內，不新增色碼）——
// 這個顏色本來就用在「可選為目標」的金框脈動，語意上跟「拉怪／被鎖定」同一個方向，沿用不新增。
// DORPG P12（契約 dorpg_p12 CONTRACT.md §1/§4：「波及傷害事件沿用既有攻擊事件加 pierce: true
// （比照 P7 濺射 splash: true），浮字『貫穿』」，任務描述：「與濺射同樣式、不同字」）：新增
// 'pierce' 語氣——槍貫穿前排打到對映後排目標的波及傷害，跟斧濺射一樣「不是這次攻擊真正鎖定的
// 目標」，同樣借用 textSecondary 灰＋small 縮小字級（見 BattleScreen.tsx pushEnemyFloat 呼叫處），
// 只是文字換成「貫穿」而不是「濺射」，讓玩家看得出這兩種附帶傷害的觸發原因不同。
// DORPG P11（契約 dorpg_p11 CONTRACT.md §1/§4：「召喚（A 以上）…浮字『召喚！』」）：新增 'summon'
// 語氣——獨立於 'taunt' 之外開一個字面值（雖然色票同樣借用金色，見下方 TONE_COLOR），讓呼叫端
// （BattleScreen.tsx 的 'summon' 事件分支）語意上跟「挑釁！」分開，不必共用同一個 tone 字面值。
export type FloatTextTone = 'damage' | 'heal' | 'shield' | 'miss' | 'buff' | 'debuff' | 'splash' | 'taunt' | 'pierce' | 'summon';

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
  /**
   * DORPG P7：較小字級（見 FloatTextTone.splash 型別註解）——目前只有濺射傷害會傳 true，其餘語氣
   * 一律沿用既有尺寸。獨立於 tone 之外開一個 prop 而不是「splash 恆小」寫死在 CSS，是因為字級跟
   * 色票是兩個獨立維度（未來若有其他語氣也想要「較不顯眼」的樣式，不必再新增 tone）。
   */
  small?: boolean;
};

const DURATION_MS = 700;

/** 十種語氣對應色票；miss/splash/pierce 共用 textSecondary（灰，語意都是「次要訊息」，見
 *  FloatTextTone 型別註解），splash/pierce 額外靠 `small` prop 縮小字級做出區隔，不需要另開
 *  一支色碼（P12：pierce 與 splash「同樣式、不同字」，色票故意共用）。buff/debuff
 *  各借一個目前色票裡還沒被其他語氣用過的顏色（mpFill 藍／criticalGlow 亮紅），跟 damage(粉紅)/
 *  heal(綠)/shield(金) 視覺上都能分開，不必新增色碼（顏色一律只從 assets.ts 的 PALETTE 來，見
 *  檔案慣例）；taunt（DORPG P10）借用 targetGold，見上方型別註解。summon（DORPG P11：「金色語氣」）
 *  同樣借用 targetGold，跟 taunt 視覺相同但字面值獨立，方便呼叫端各自語意清楚。 */
const TONE_COLOR: Record<FloatTextTone, string> = {
  damage: PALETTE.hpEnemy,
  heal: PALETTE.hpNormal,
  shield: PALETTE.borderGold,
  miss: PALETTE.textSecondary,
  buff: PALETTE.mpFill,
  debuff: PALETTE.criticalGlow,
  splash: PALETTE.textSecondary,
  taunt: PALETTE.targetGold,
  pierce: PALETTE.textSecondary,
  summon: PALETTE.targetGold,
};

export default function FloatText({ text, tone, reducedMotion = false, anchorPx, small = false }: FloatTextProps) {
  const [visible, setVisible] = useState(true);

  useEffect(() => {
    const id = window.setTimeout(() => setVisible(false), DURATION_MS);
    return () => window.clearTimeout(id);
  }, []);

  if (!visible) return null;

  if (anchorPx) {
    // 定點模式（見 FloatTextProps.anchorPx 註解）：位置由呼叫端算好的像素座標決定，動畫走
    // transform（.point 系列 class），不依賴容器高度。
    const cls = [styles.point, reducedMotion && styles.pointReduced, small && styles.pointSmall].filter(Boolean).join(' ');
    return (
      <div
        className={cls}
        style={{ left: anchorPx.x, top: anchorPx.y, color: TONE_COLOR[tone] }}
        aria-hidden="true"
      >
        {text}
      </div>
    );
  }

  const cls = [styles.text, reducedMotion && styles.reduced, small && styles.textSmall].filter(Boolean).join(' ');
  return (
    <div
      className={cls}
      style={{ color: TONE_COLOR[tone] }}
      aria-hidden="true"
    >
      {text}
    </div>
  );
}
