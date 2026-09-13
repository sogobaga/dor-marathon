// DORPG 怪物精靈播放器（移植自內容包 source/ui/08_DORPG_Content_Pack_v1/runtime/sprite-player.js 的
// DORPGSpritePlayer class）。原版是 vanilla Canvas + rAF class，這裡改寫成 React hook：
// - 播放狀態（elapsed/def/img/fired…）全放 useRef，不透過 setState 驅動，因為畫面是逐幀 canvas 命令式繪製，
//   若每幀 setState 會讓整棵元件樹跟著重繪、拖垮戰鬥畫面 FPS（P0/契約 §7 每幀都會 tick 戰鬥引擎）。
// - 圖片快取沿用原版「模組級 Map」設計：同一張圖集被多隻同款怪／同一隻怪的多個畫面共用時只下載解碼一次。
// - 新增 request 計數（沿用原版 this.request）防止「切動作太快、舊的 loadImage 才回來」把畫面蓋回舊動作。
//
// 契約（scratchpad/dorpg_p1/CONTRACT.md §5）只規定 MonsterAnimSet 的欄位形狀，實際型別由 ASSETS 工作者
// 寫進 src/lib/dorpg/monsterAnim.ts；本檔完稿當下該檔可能還不存在，所以在這裡自行定義一份結構相同的
// `SpriteAnimSet`／`SpriteAnimDef`／`SpriteEvent` 並 export，MonsterSprite.tsx 直接吃 MONSTER_ANIMS[id]
// 傳進來（TS 結構化型別，只要欄位形狀一致就相容，不需要兩邊 import 同一個型別名）。

import { useCallback, useEffect, useRef, useState, type RefObject } from 'react';

/** 怪物四個動作鍵；與契約 §5 MonsterSprite props 的 action 完全同名。 */
export type SpriteActionKind = 'idle' | 'attack' | 'hit' | 'death';

export type SpriteFrame = {
  index: number;
  x: number;
  y: number;
  w: number;
  h: number;
  durationMs: number;
};

/** 視覺同步事件（README：純視覺提示，不是扣血依據）；契約欄位名為 name，內容包原始 JSON 用的是 event，轉換由 ASSETS 負責。 */
export type SpriteEvent = {
  name: string;
  atMs: number;
};

export type SpriteAnimDef = {
  /** 圖集相對路徑；本 hook 不使用這欄（改用呼叫端傳入的 sheetUrlFor 取得完整 CDN URL），只為結構相容保留。 */
  sheet?: string;
  columns: number;
  rows: number;
  frameCount: number;
  durationMs: number;
  loop: boolean;
  /** 播完後自動接的下一個動作（例：attack/hit → idle）；death 為 null/undefined，停在末格。 */
  next?: SpriteActionKind | null;
  /** README 的宣告欄位：death 停末格的語意已由 next=null 隱含實現（見下方 tick 的「非 loop 一律畫末格」），這裡只保留欄位、不額外判斷。 */
  holdLast?: boolean;
  events?: SpriteEvent[];
  frames: SpriteFrame[];
};

export type SpriteAnimSet = {
  anchor: { x: number; y: number };
  frameSize: { w: number; h: number };
  animations: Record<SpriteActionKind, SpriteAnimDef>;
};

/** 缺資料時的退路（ASSETS 尚未收錄某隻怪，或呼叫端傳錯 id）：四個動作都只有一格 512×512，讓 hook 邏輯照跑不炸。
 *  實際圖片多半會 404（sheetUrlFor 仍會組出一個 URL），載入失敗會被 loadImage 的 catch 吃掉、維持顯示 poster —— 見 MonsterSprite。 */
const EMPTY_FRAME: SpriteFrame = { index: 0, x: 0, y: 0, w: 512, h: 512, durationMs: 1000 };
const EMPTY_ANIM: SpriteAnimDef = { columns: 1, rows: 1, frameCount: 1, durationMs: 1000, loop: true, frames: [EMPTY_FRAME] };
export const FALLBACK_ANIMS: SpriteAnimSet = {
  anchor: { x: 0.5, y: 0.87890625 },
  frameSize: { w: 512, h: 512 },
  animations: { idle: EMPTY_ANIM, attack: EMPTY_ANIM, hit: EMPTY_ANIM, death: EMPTY_ANIM },
};

export type SpritePlayOptions = {
  onEvent?(name: string): void;
  onComplete?(): void;
};

// 模組級圖片快取（比照原版 `const images=new Map()`）：key 是完整 URL，跨所有 useSpritePlayer 實例共用，
// 五隻怪同時上場、同一隻怪的待機/戰鬥雙畫面都不必重複下載。
const imageCache = new Map<string, Promise<HTMLImageElement>>();

function loadImage(url: string): Promise<HTMLImageElement> {
  let entry = imageCache.get(url);
  if (!entry) {
    entry = new Promise<HTMLImageElement>((resolve, reject) => {
      const img = new Image();
      // 刻意不設 img.crossOrigin：R2（img.dor.tw）目前尚未設定 CORS，r2.env 的 token 也沒有 bucket
      // 層級權限去補（ASSETS 工作者已確認）。設了 crossOrigin='anonymous' 但來源沒回 CORS header，
      // 瀏覽器會直接判定圖片載入失敗（onerror），永遠退回 poster、動畫播不出來。
      // 這裡只用 drawImage 把圖畫上 canvas、不讀像素（沒有 getImageData／toDataURL／toBlob），
      // canvas 被瀏覽器標記 tainted 對我們沒有影響。
      // 之後若要新增「讀取畫布像素」的功能（例如 getImageData 做像素檢查、或截圖匯出），
      // 才需要加回 img.crossOrigin='anonymous'，且要先請 ASSETS／有權限的人在 Cloudflare R2
      // 的 bucket 設定裡補上 CORS（AllowedOrigins 含 https://www.dor.tw 等，見契約 §3），否則加了只會讓圖全部載入失敗。
      img.onload = () => resolve(img);
      img.onerror = () => {
        imageCache.delete(url); // 失敗不快取，允許之後重試（例如網路暫時中斷）
        reject(new Error(`Cannot load ${url}`));
      };
      img.src = url;
    });
    imageCache.set(url, entry);
  }
  return entry;
}

type PlayerState = {
  action: SpriteActionKind;
  def: SpriteAnimDef | null;
  img: HTMLImageElement | null;
  elapsed: number;
  done: boolean;
  fired: Set<number>;
  last: number;
  request: number;
  raf: number;
  destroyed: boolean;
  onEvent?: (name: string) => void;
  onComplete?: () => void;
};

function createState(): PlayerState {
  return {
    action: 'idle',
    def: null,
    img: null,
    elapsed: 0,
    done: false,
    fired: new Set<number>(),
    last: 0,
    request: 0,
    raf: 0,
    destroyed: false,
  };
}

export type UseSpritePlayerResult = {
  play(action: SpriteActionKind, o?: SpritePlayOptions): void;
  current: string;
  /** 額外欄位（契約只要求 play/current，這裡加一個給 MonsterSprite 判斷「圖集是否已成功畫出第一幀」）：
   *  true = canvas 已有可看的畫面，可以蓋掉 poster；載入失敗會被設回 false，讓元件退回顯示 poster。 */
  ready: boolean;
};

/**
 * 把內容包 runtime/sprite-player.js 的播放邏輯移植成 hook。
 * canvasRef 由呼叫端建立（MonsterSprite）；本 hook 只負責：
 *  - 依 frameSize 設定 canvas 的實體解析度（× DPR）並 ctx.scale，讓邏輯座標永遠是 512² 那份幀資料。
 *  - rAF 迴圈推進 elapsed／觸發 events／換幀／loop 或接 next／document.hidden 時 dt=0。
 */
export function useSpritePlayer(
  canvasRef: RefObject<HTMLCanvasElement | null>,
  anims: SpriteAnimSet,
  sheetUrlFor: (action: SpriteActionKind) => string,
  opts: { reducedMotion?: boolean } = {},
): UseSpritePlayerResult {
  const [current, setCurrent] = useState<SpriteActionKind>('idle');
  const [ready, setReady] = useState(false);

  const stateRef = useRef<PlayerState>(createState());
  const animsRef = useRef(anims);
  animsRef.current = anims;
  const reducedMotionRef = useRef(!!opts.reducedMotion);
  reducedMotionRef.current = !!opts.reducedMotion;
  const sheetUrlForRef = useRef(sheetUrlFor);
  sheetUrlForRef.current = sheetUrlFor;

  // 依 frameSize × DPR 設定畫布實體解析度；CSS 顯示尺寸（size×size）交給呼叫端用 style 控制，
  // 這裡只保證「畫上去的座標系統」永遠是內容包資料的邏輯 512²（契約 §5：邏輯畫布縮放到 size CSS px，DPR 對應）。
  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas) return;
    const dpr = typeof window !== 'undefined' ? window.devicePixelRatio || 1 : 1;
    const { w, h } = anims.frameSize;
    canvas.width = Math.max(1, Math.round(w * dpr));
    canvas.height = Math.max(1, Math.round(h * dpr));
    const ctx = canvas.getContext('2d');
    ctx?.scale(dpr, dpr);
  }, [canvasRef, anims.frameSize.w, anims.frameSize.h]);

  const draw = useCallback(
    (index: number) => {
      const canvas = canvasRef.current;
      const st = stateRef.current;
      if (!canvas || !st.def || !st.img) return;
      const frame = st.def.frames[index];
      if (!frame) return;
      const ctx = canvas.getContext('2d');
      if (!ctx) return;
      // 目的地永遠畫在邏輯座標 (0,0,frame.w,frame.h)：canvas 的實體像素縮放已由上面的 ctx.scale(dpr) 處理。
      ctx.clearRect(0, 0, frame.w, frame.h);
      ctx.drawImage(st.img, frame.x, frame.y, frame.w, frame.h, 0, 0, frame.w, frame.h);
    },
    [canvasRef],
  );

  const play = useCallback(
    (action: SpriteActionKind, o?: SpritePlayOptions) => {
      const st = stateRef.current;
      const def = animsRef.current.animations[action];
      if (!def) return; // 資料缺這個動作：安靜忽略，不讓單一資料錯誤砸掉整個戰場
      st.onEvent = o?.onEvent;
      st.onComplete = o?.onComplete;
      const request = ++st.request;
      setCurrent(action);

      const reduced = reducedMotionRef.current;
      loadImage(sheetUrlForRef.current(action))
        .then((img) => {
          if (st.destroyed || request !== st.request) return; // 競態：載入完成前又切了動作，或元件已卸載
          st.action = action;
          st.def = def;
          st.img = img;
          st.elapsed = 0;
          // 減少動態：只畫第一格、之後 tick 不再推進（見下方 tick 的 !st.done 判斷）。
          st.done = !!reduced;
          st.fired = new Set();
          st.last = performance.now();
          draw(0);
          setReady(true);
        })
        .catch(() => {
          // 圖集載入失敗（契約 §5：退回 poster）：只在這仍是最新請求時才回報，避免蓋掉之後成功的新請求。
          if (request === st.request) setReady(false);
        });
    },
    [draw],
  );

  useEffect(() => {
    const st = stateRef.current;
    st.destroyed = false;
    st.last = performance.now();

    function tick(now: number) {
      if (st.destroyed) return;
      // 分頁切到背景：dt=0（不補幀，回前景從原進度接續），比照原版 document.hidden 判斷。
      const dt = typeof document !== 'undefined' && document.hidden ? 0 : Math.min(100, now - st.last);
      st.last = now;

      if (st.def && !st.done) {
        st.elapsed += dt;
        const d = st.def;
        const events = d.events || [];
        for (let i = 0; i < events.length; i++) {
          const e = events[i];
          if (st.elapsed >= e.atMs && !st.fired.has(i)) {
            st.fired.add(i);
            st.onEvent?.(e.name);
          }
        }
        if (st.elapsed >= d.durationMs) {
          if (d.loop) {
            st.elapsed %= d.durationMs;
            st.fired = new Set();
          } else {
            // 非循環動作播完：畫末格並停住（death 靠這條停在最後一格），呼叫 onComplete 後才接 next。
            draw(d.frameCount - 1);
            st.done = true;
            st.onComplete?.();
            if (d.next) play(d.next);
          }
        }
        if (!st.done) {
          let time = 0;
          let index = d.frameCount - 1;
          for (let i = 0; i < d.frames.length; i++) {
            time += d.frames[i].durationMs;
            if (st.elapsed < time) {
              index = i;
              break;
            }
          }
          draw(index);
        }
      }

      st.raf = requestAnimationFrame(tick);
    }

    st.raf = requestAnimationFrame(tick);
    return () => {
      st.destroyed = true;
      ++st.request; // 讓任何仍在飛行中的 loadImage.then 視為過期
      cancelAnimationFrame(st.raf);
    };
    // draw/play 皆為 useCallback 且依賴穩定（canvasRef 不變、sheetUrlFor 走 ref），此 effect 只需在 mount 時跑一次。
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  return { play, current, ready };
}
