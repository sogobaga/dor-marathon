'use client';

// DORPG 怪物精靈（P1）：poster 先顯示（避免圖集下載完成前畫面空白）→ 圖集載入成功後淡入 canvas 接手播放。
// 幀資料／URL 由 ASSETS 工作者的 monsterAnim.ts（MONSTER_ANIMS）與 cdn.ts（monsterSheetUrl）提供；
// 兩者與本檔同一輪撰寫，寫這份檔案當下可能還不存在——用 useSpritePlayer 匯出的 SpriteAnimSet 結構相容型別
// 接住即可（見 lib/dorpg/useSpritePlayer.ts 開頭說明），不需要等對方檔案落地才能通過型別檢查。
import { useCallback, useEffect, useMemo, useRef } from 'react';
import { monsterSheetUrl } from '@/lib/dorpg/cdn';
import { MONSTER_ANIMS, type MonsterAnim, type MonsterAnimSet } from '@/lib/dorpg/monsterAnim';
import { FALLBACK_ANIMS, useSpritePlayer, type SpriteActionKind, type SpriteAnimDef, type SpriteAnimSet } from '@/lib/dorpg/useSpritePlayer';
import styles from './MonsterSprite.module.css';

// ASSETS 的 monsterAnim.ts 逐字抄 monster.json 的原始鍵名（frameSize.width/height、events[].event），
// 與本檔 useSpritePlayer 自訂的 canonical 形狀（frameSize.w/h、events[].name）欄位名不同、不是結構相容——
// 兩個工作者是各自獨立寫的檔案，這裡用一個小轉接函式把「內容包原始形狀」轉成 hook 吃的形狀，
// 讓 useSpritePlayer.ts 保持不需要認識 monsterAnim.ts 的內部欄位命名慣例（沒有雙向 import 依賴）。
function adaptAnim(a: MonsterAnim): SpriteAnimDef {
  return {
    columns: a.columns,
    rows: a.rows,
    frameCount: a.frameCount,
    durationMs: a.durationMs,
    loop: a.loop,
    next: a.next,
    holdLast: a.holdLast,
    events: a.events.map((e) => ({ name: e.event, atMs: e.atMs })),
    frames: a.frames,
  };
}

function adaptAnimSet(real: MonsterAnimSet): SpriteAnimSet {
  return {
    anchor: real.anchor,
    frameSize: { w: real.frameSize.width, h: real.frameSize.height },
    animations: {
      idle: adaptAnim(real.animations.idle),
      attack: adaptAnim(real.animations.attack),
      hit: adaptAnim(real.animations.hit),
      death: adaptAnim(real.animations.death),
    },
  };
}

export type MonsterSpriteProps = {
  monsterId: string;
  action: SpriteActionKind;
  /** 正方形 CSS px；邏輯畫布固定 512²（見 useSpritePlayer 的 DPR 換算），這裡只決定顯示大小。 */
  size: number;
  onActionEvent?(name: string): void;
  onActionComplete?(action: SpriteActionKind): void;
  /** 減少動態：只顯示各動作第一格，不跑動畫。 */
  reducedMotion?: boolean;
  /** 圖集載入前／載入失敗時顯示的靜態圖（idle 第 0 格 512²，含 alpha）。 */
  posterUrl: string;
};

export default function MonsterSprite({
  monsterId,
  action,
  size,
  onActionEvent,
  onActionComplete,
  reducedMotion = false,
  posterUrl,
}: MonsterSpriteProps) {
  const canvasRef = useRef<HTMLCanvasElement>(null);

  // MONSTER_ANIMS 若尚未收錄這隻怪（ASSETS 還沒寫完，或 id 打錯），退回 FALLBACK_ANIMS：
  // hook 一律要拿到「形狀正確」的資料才不會在效果內炸掉；真正的圖集會 404，載入失敗自然退回 poster。
  // 找得到才需要轉接形狀（adaptAnimSet），避免每次渲染都重建一份新物件（identity 穩定才不會誤觸重播，見下方 sheetUrlFor 的理由）。
  const real = MONSTER_ANIMS[monsterId];
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const anims = useMemo(() => (real ? adaptAnimSet(real) : FALLBACK_ANIMS), [monsterId, real]);

  // 用 useCallback 釘住 identity：只依 monsterId 變化。若每次渲染都重建這個函式，
  // 下面 action-effect 的依賴（play 由它組成）就會跟著每次渲染變身，讓「HP 變動之類的無關重繪」
  // 也被誤判成「動作變更」而重播一次動畫、蓄圖閃爍。
  const sheetUrlFor = useCallback((a: SpriteActionKind) => monsterSheetUrl(monsterId, a), [monsterId]);

  const { play, ready } = useSpritePlayer(canvasRef, anims, sheetUrlFor, { reducedMotion });

  // 回呼放 ref：避免呼叫端每次渲染傳新的 inline function 也讓 play 的依賴跟著變動。
  const onEventRef = useRef(onActionEvent);
  onEventRef.current = onActionEvent;
  const onCompleteRef = useRef(onActionComplete);
  onCompleteRef.current = onActionComplete;

  useEffect(() => {
    play(action, {
      onEvent: (name) => onEventRef.current?.(name),
      onComplete: () => onCompleteRef.current?.(action),
    });
    // reducedMotion 切換時也要重播一次，才能在「照樣動畫」與「定格首格」之間切換。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [action, reducedMotion, play]);

  return (
    <div className={styles.root} style={{ width: size, height: size }}>
      {/* eslint-disable-next-line @next/next/no-img-element */}
      <img
        className={styles.layer}
        style={{ opacity: ready ? 0 : 1 }}
        src={posterUrl}
        alt=""
        draggable={false}
        aria-hidden="true"
      />
      <canvas ref={canvasRef} className={styles.layer} style={{ width: size, height: size, opacity: ready ? 1 : 0 }} aria-hidden="true" />
    </div>
  );
}
