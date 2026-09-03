import type { Metadata, Viewport } from 'next'
import { cache } from 'react'
import './globals.css'
import InAppBrowserNotice from '@/components/InAppBrowserNotice'
import InterstitialAd from '@/components/InterstitialAd'
import LandscapeNotice from '@/components/LandscapeNotice'
import Analytics from '@/components/Analytics'
import AppProviders from '@/components/AppProviders'
import ViewportHeightFix from '@/components/ViewportHeightFix'
import ViewportDebug from '@/components/ViewportDebug'
import PwaInstallPrompt from '@/components/PwaInstallPrompt'
import UpdateNotice from '@/components/UpdateNotice'
import BounceCleanup from '@/components/BounceCleanup'
import { veilColorsOf } from '@/lib/skinColors'

// 各 skin 的瀏覽器 chrome（狀態列）色；新增 skin 時在此與 globals.css/appSettings/後端 specs 一併加。
const SKIN_THEME_COLOR: Record<string, string> = { default: '#09090f', warm: '#FBF4E9', warm2: '#FBF5EA' }

// 伺服器端讀取前台公開系統設定（skin、favicon…）：直接寫進 SSR，第一次繪製就正確、不靠 localStorage。
// React cache：同一請求只查一次；fetch 快取拉到 300 秒（原 30 秒，2026-09-03 為降低 Neon 夜間空轉喚醒——
// 這支端點跑在「每一個 request」的 RootLayout 上，是喚醒 Neon 最大宗的來源）。skin/favicon 極少變動，
// 後台改了最多等 5 分鐘於「下次載入」生效，可接受。逾時/失敗回空 → 用預設。
const getPublicSettings = cache(async (): Promise<Record<string, string>> => {
  try {
    const base = process.env.API_URL || 'http://localhost:8080'
    const res = await fetch(`${base}/api/v1/app-settings/public`, { next: { revalidate: 300 }, signal: AbortSignal.timeout(2500) })
    if (!res.ok) return {}
    const j = await res.json()
    return (j?.settings as Record<string, string>) || {}
  } catch {
    return {}
  }
})

function skinOf(s: Record<string, string>): string {
  const v = s.active_skin
  return typeof v === 'string' && SKIN_THEME_COLOR[v] && v !== 'default' ? v : 'default'
}

export async function generateMetadata(): Promise<Metadata> {
  const s = await getPublicSettings()
  const fav = s.favicon_url // 後台可自訂的瀏覽器分頁 favicon（未設 → 用內建 icon）
  return {
    title: 'DOR 城市探索',
    description: '一場把城市變成賽道的跑步挑戰——不用站上起跑線，也能用每一次出門，跑出屬於自己的完賽故事。',
    manifest: '/manifest.json',
    icons: {
      icon: fav ? [{ url: fav }] : [
        { url: '/icon-192.png', sizes: '192x192', type: 'image/png' },
        { url: '/icon-512.png', sizes: '512x512', type: 'image/png' },
      ],
      // iOS「加入主畫面」的圖示：**固定用 /apple-touch-icon.png，刻意不吃後台的 favicon_url**。
      // favicon 通常是 16~48px 的分頁小圖，拿去當主畫面 App 圖示會糊掉/變形（曾實際發生）；
      // 兩者用途不同，不該共用同一個設定。要換 App 圖示請替換 public/apple-touch-icon.png。
      apple: '/apple-touch-icon.png',
    },
    appleWebApp: { capable: true, statusBarStyle: 'black-translucent', title: 'DOR' },
    openGraph: {
      title: 'DOR｜城市探索',
      description: '一場把城市變成賽道的跑步挑戰——不用站上起跑線，也能用每一次出門，跑出屬於自己的完賽故事。',
      url: 'https://www.dor.tw',
      siteName: 'DOR｜城市探索',
      locale: 'zh_TW',
      type: 'website',
      images: [{ url: 'https://www.dor.tw/brand-hero-og-v3.jpg', width: 1200, height: 628, alt: 'DOR｜城市探索——把城市，變成你的遊戲場' }],
    },
    twitter: {
      card: 'summary_large_image', // 有 1200×630 主視覺後改大圖卡（原 summary 小方圖）
    },
  }
}

export async function generateViewport(): Promise<Viewport> {
  const skin = skinOf(await getPublicSettings())
  return {
    width: 'device-width',
    initialScale: 1,
    maximumScale: 1,
    userScalable: false,
    viewportFit: 'cover',
    // 明文宣告「虛擬鍵盤只縮 visual viewport、不縮版面」——這正是本站要的行為。
    // ・iOS：WebKit 尚未實作（bug 259770，2023 開單至今 NEW）→ 這行是 no-op，不會壞任何事；
    //   而 iOS 現行預設本來就等同 resizes-visual。
    // ・Android Chrome 108+ 預設同樣是 resizes-visual → 明寫等於把行為釘死，不怕未來預設值變動，
    //   並可救回舊 Chrome/WebView 那種「鍵盤壓縮 layout viewport」的預設。
    // ⚠️ 永遠不要改成 resizes-content：那就是症狀 B 的規格化版本。
    // ⚠️ 也不要用 overlays-content：會奪走瀏覽器「自動捲到焦點輸入框」的能力。
    interactiveWidget: 'resizes-visual',
    themeColor: SKIN_THEME_COLOR[skin] || SKIN_THEME_COLOR.default,
  }
}

// 開機腳本（body 第一個 <script>，在任何內容繪製前同步執行；try/catch 全包）。兩件事：
//
// A. 載入瞬間快照 → window.__dorVis（?vpdebug=1 面板讀）：能見度／視窗高度／導航類型、第一次 visible 與第一次
//    resize 的時間點，以及下面 B 的決策結果（ar 欄）。
//
// B. 到站自動重載（症狀 A 的治療；2026-09-02 定案）：iOS Safari 從外部到站（QR 掃碼／連結／輸入網址／分頁還原）
//    100% 重現「root 圖層被合成器上移 lvh−svh、所有量測值卻正確」的病態；使用者實測程式化 location.reload() 能治
//    （切頁槓桿在賽事落地頁失效）、且提議「白幕＋自動刷新＋小動畫掩飾閃爍」。做法：在第一次繪製前先蓋上與 skin 同色
//    的白幕（含轉圈），並把 html/body 背景染成同色讓圖層外的帶子看不出來，等分頁可見後 0.4s 重載（v766 起改為 replace 同網址，見 E）；重載後的載入
//    nav=reload 不再觸發，畫面直接正常。觸發範圍刻意收窄，站內換頁完全不受影響：
//      ・只在 iPhone 的 WebKit（排除 PWA standalone、Android、桌機；**不再依 UA 排除任何一家瀏覽器**）
//        ⚠️ v759 真機定案：會發病的到站文件是 Chrome iOS（面板 ua 有 CriOS/152），使用者手機預設瀏覽器是 Chrome、
//        掃 QR 開到的一直是 Chrome 而非 Safari（UI 幾乎一樣）；v753–758 的 Safari 閘門／排 CriOS 把病人全判成
//        skip:not-safari，自癒從未在真實掃碼路徑上發動過。Safari 本體是否發病無真機證據。iPhone 上所有瀏覽器
//        都是 WebKit，多做一次代價很低，所以不猜。PWA 由 navigator.standalone 排除並順手種 cookie dor_pwa=1
//        給 middleware（standalone 有獨立 cookie jar）。詳見 src/lib/arrivalBounce.ts。
//      ・本次載入是 navigate／back_forward（reload 不算），且 referrer 為空或外站（站內整頁跳轉不算）
//      ・同一分頁 2 分鐘內只做一次（sessionStorage；Safari 還原分頁會連 sessionStorage 一起還原，隔夜回來會再做）
//      ・排除 /track（跑步恢復不能被打斷）、帶 code=/state=/token= 的授權回跳網址（一次性授權碼禁不起重載）
//      ・逃生口：?vpfix=off 或 ?noreload=1
//    ⚠️ v754 補上「分頁還原」路徑（使用者回報 v753 沒治到）：症狀 A 有兩條發生路徑，v753 只涵蓋第一條——
//    ① 外部到站（QR/連結/輸入網址）：nav=navigate|back_forward 且 referrer 為空或外站。
//    ② 分頁還原：分頁被 Safari 回收後回來，會整份重新載入（畫面先卡一下再出現，出現時就已跑版）。
//       這一次載入的 nav 是 reload/back_forward、referrer 是站內，正好被 v753 的 nav=reload 與 internal-ref
//       兩個排除條件擋掉，所以完全沒開火。改用 sessionStorage 的 dor.tabSeen（每次載入寫入時間戳、隨分頁還原
//       一起被 Safari 還原）判定：同一分頁距上次載入 > 5 分鐘的整份重載＝還原路徑。
//    防迴圈有兩道獨立保險：我方重載後的新文件 dor.tabSeen 只差幾百毫秒（不構成 restore、且非外部到站 →
//    skip:no-trigger），再加上 dor.arrivalReload 的 2 分鐘節流。
//    另提供 window.__dorVeilReload(why)：給 ViewportHeightFix 的「觸點越界」偵測當備援（重載後仍壞時的第二道）。
//    6 秒保險：若 reload 因故沒發生，自動移除白幕，頁面不會被永久蓋住。
//    ⚠️ v757 起主要治療改由 src/middleware.ts 的「到站彈跳頁」在伺服器端接手：符合條件的到站請求直接回一份
//    ~1KB 品牌色彈跳頁（含轉圈），600ms 後 location.replace() 同網址；那次 replace 是同源導覽，middleware 判定
//    後放行、直接吃到治好的第二次載入，使用者完全不會看到完整 app 載入兩次。以下這段 boot-script 重載改為
//    「備援」：只涵蓋 middleware 判斷不到的路徑（iOS < 16.4 無 Sec-Fetch-* 標頭、分頁還原、bot/monitor UA 等
//    middleware 主動放行的情境）。由 middleware 彈跳頁 replace 過來的文件帶 cookie dor_b=1，本段落必須認得
//    這個記號並跳過（見下方 skip='bounced'），否則會變成「彈跳後又整份重載」的雙重延遲。
//
// D. 分頁回收後從快取還原（v764，Railway HTTP log 重建 2026-09-03 08:35–08:37 真機時間軸）：
//    08:35:29 首頁到站 → middleware 彈跳（825B）→ replace 載入 app（5KB）→ 治好；使用者切去別的分頁／掃活動頁 QR；
//    08:37:10 首頁分頁「重新開機」（auth/me、interstitial、races… 整組 boot 請求全部重打、蓋板廣告再彈一次）
//    ——但伺服器**完全沒有收到首頁 HTML 請求**。唯一解釋：Chrome iOS 在記憶體壓力下回收了該分頁的 WKWebView，
//    切回時以「歷史導覽」重建，HTML 直接由瀏覽器快取供應（首頁回應是 s-maxage=30、無 no-store，WebKit 對
//    back/forward 一律先用快取），middleware 根本沒機會彈跳。這份新文件：nav=back_forward、referrer＝彈跳頁的
//    同源網址（v757 起所有到站文件的 referrer 都長這樣）、sessionStorage 全新（Chrome iOS 重建 WKWebView 不帶
//    sessionStorage → tabSeen=0、蓋板 SEEN 也沒了）→ 舊規則三條全不中（非 external、tabSeen=0）→ skip:no-trigger
//    → 這份「WKWebView 重建後的第一份文件」病態卻沒人治。v757 之前到站文件 referrer 是空的、還原時會走 arrival，
//    是彈跳頁把 referrer 變成同源才打開這個洞。規則：nav=back_forward 一律視為還原並重載一次（站內跨文件返回若
//    bfcache 沒接住也會多重載一次，代價可接受；bfcache 接住的不會跑到這段）。/track 仍排除、2 分鐘節流仍有效。
//    v.ts＝navigation transferSize（快取供應為 0）進 ?vpdebug=1 面板，下次真機可直接看到「零網路載入」的證據。
// E. 治療手段從 location.reload() 改為 location.replace(同網址)（v766，第七輪；Railway HTTP log 重建 2026-09-03
//    01:44:42–01:47 真機時間軸）：賽事頁 /event/<slug> 是 no-store（每份文件都是 Railway 請求，不可能快取供應），
//    log 裡同一賽事頁相隔 0.72 秒被抓兩次（皆 7292B、同一 iPhone）、之後只有一組 boot 請求，面板顯示的病態文件
//    nav=reload、referrer 同源、ar=skip:no-trigger。也就是：第一份文件才剛開機就被程式化 reload（本段 arrival/
//    restore 或觸點越界 tap），reload 出來的第二份文件**仍然是病的**——早期程式化 reload 在這條路徑上治不好，
//    v753 起「reload 實測有效」的前提不成立（當時有效的很可能是使用者手動下拉／等畫面穩定後的 reload）。
//    工作假說：WebKit 對 reload 與 back/forward 兩種 load type 會還原 HistoryItem 上保存的捲動＋view state
//    （iOS restoreScrollPositionAndViewState），病態幾何跟著被帶進新文件；location.replace(同網址) 是
//    FrameLoadType::Same，不做 view-state 還原——這正是 middleware 彈跳頁用的手段、而且在第五輪真機驗收有效。
//    因此 renav()：有 hash 時仍用 reload（同網址帶片段的 replace 會被當成頁內片段導覽、不會重新載入），否則
//    replace(href)。replace 出來的新文件 nav=navigate、referrer 同源 → no-trigger，2 分鐘節流照舊，不會迴圈。
//    診斷補強：v.ts（transferSize）在 CriOS 對網路供應的文件也回 0，**不能**再當快取供應的證據；每份文件離開決策
//    後把「本份的 ar|nav|referrer host」寫進 sessionStorage dor.prevAr，下一份文件開機讀成 v.prev 進面板，真機
//    就能看出「病態文件是誰生的、上一份做了什麼決定」；v.sa／v.la＝距上次 tabSeen／上次治療的毫秒數。
// C. bounced 標記：由彈跳頁 replace 過來的文件 referrer 是同源（strict-origin-when-cross-origin 對同源給完整網址），
//    本來就落在 no-trigger；帶 dor_b=1 cookie 時只是把標籤改成 skip:bounced 讓 ?vpdebug=1 面板看得出走了哪條路。
//    刻意「不」把 cookie 當成跳過重載的理由：cookie 跨分頁共享（5 秒），同一支手機 5 秒內再掃一張 QR 時 middleware
//    會因 cookie 放行、那份文件仍是病態的到站（referrer 空）→ 這裡必須照常走 arrival 重載，否則沒人治它。
//    ⚠️ 白幕刻意「不插任何 DOM 節點」：本站是 React 18.3，hydration 對 <body> 子節點嚴格比對，多塞一個 <div>
//    會觸發 hydration mismatch → 整棵樹客端重繪（白幕也被拔掉）。改用 constructable stylesheet
//    （document.adoptedStyleSheets）把白幕畫在 html::before／轉圈畫在 html::after——純 CSS 偽元素，React 看不見。
//    iOS < 16.4 沒有 CSSStyleSheet 建構子 → catch 掉、沒轉圈但仍會染色＋重載。
function bootJs(bg: string, fg: string): string {
  return `(function(){try{
var d=document,p=performance,w=window,n=navigator;
var v={l:d.visibilityState,t:Math.round(p.now()),pr:!!d.prerendering,vt:-1,n:'',ih:w.innerHeight,ch:d.documentElement.clientHeight,sh:(w.screen||{}).height||0,rt:-1,rih:0,ar:'',ts:-1,prev:'',sa:-1,la:-1};
var e=p.getEntriesByType&&p.getEntriesByType('navigation')[0];if(e){v.n=e.type;v.ts=typeof e.transferSize==='number'?e.transferSize:-1}
if(v.l==='visible')v.vt=v.t;else d.addEventListener('visibilitychange',function f(){if(d.visibilityState==='visible'){v.vt=Math.round(p.now());d.removeEventListener('visibilitychange',f)}});
w.addEventListener('resize',function r(){v.rt=Math.round(p.now());v.rih=w.innerHeight;w.removeEventListener('resize',r)});
w.__dorVis=v;
var BG='${bg}',FG='${fg}';
var veil=false,done=false;
function showVeil(){
  if(veil)return;veil=true;
  d.documentElement.style.background=BG;if(d.body)d.body.style.background=BG;
  try{
    var css='@keyframes dorspin{to{transform:rotate(360deg)}}'
      +'html::before{content:"DOR 載入中…";position:fixed;top:-40vh;left:0;right:0;height:180vh;z-index:2147483646;background:'+BG+';color:'+FG+';font:600 12px/1 -apple-system,system-ui,sans-serif;letter-spacing:.08em;display:flex;align-items:center;justify-content:center;box-sizing:border-box;padding-top:64px}'
      +'html::after{content:"";position:fixed;left:50%;top:50%;width:30px;height:30px;margin:-24px 0 0 -15px;border:3px solid '+FG+'33;border-top-color:'+FG+';border-radius:50%;animation:dorspin .8s linear infinite;z-index:2147483647}';
    var sh=new CSSStyleSheet();sh.replaceSync(css);d.adoptedStyleSheets=[sh];
    setTimeout(function(){try{d.adoptedStyleSheets=[]}catch(x){}},6000);
  }catch(x){}
}
function refHost(){return ref?(ref.split('/')[2]||ref):'-'}
function mark(){try{sessionStorage.setItem('dor.prevAr',(v.ar||'-')+'|'+(v.n||'?')+'|'+refHost())}catch(x){}}
function renav(){var L=w.location;try{if(L.hash){L.reload();return}L.replace(L.href)}catch(x){try{L.reload()}catch(y){}}}
function reload(why){
  if(done)return;done=true;
  try{sessionStorage.setItem('dor.arrivalReload',String(Date.now()))}catch(x){}
  v.ar=why+'@'+Math.round(p.now());mark();
  showVeil();
  var go=function(){setTimeout(renav,400)};
  if(d.visibilityState==='visible')go();else d.addEventListener('visibilitychange',function g(){if(d.visibilityState==='visible'){d.removeEventListener('visibilitychange',g);go()}});
}
w.__dorVeilReload=function(why){reload(why||'manual')};
var ua=n.userAgent||'';
var q=w.location.search+w.location.hash,path=w.location.pathname;
var now=Date.now(),seen=0,last=0;
try{seen=+sessionStorage.getItem('dor.tabSeen')||0;last=+sessionStorage.getItem('dor.arrivalReload')||0}catch(x){}
var prev='';try{prev=sessionStorage.getItem('dor.prevAr')||''}catch(x){}
v.prev=prev;v.sa=seen?now-seen:-1;v.la=last?now-last:-1;
try{sessionStorage.setItem('dor.tabSeen',String(now))}catch(x){}
var ref=d.referrer||'',org=w.location.origin;
var external=!ref||(ref!==org&&ref.indexOf(org+'/')!==0);
var nt=v.n||'navigate';
var why='';
if((nt==='navigate'||nt==='back_forward')&&external)why='arrival';
else if(nt==='back_forward')why='restore';
else if(seen&&now-seen>300000)why='restore';
var skip='';
if(!/iPhone|iPod/.test(ua))skip='not-iphone';
else if(n.standalone===true){skip='standalone';try{d.cookie='dor_pwa=1;Max-Age=31536000;Path=/;SameSite=Lax'}catch(x){}}
else if(/vpfix=off|noreload=1/.test(q))skip='opt-out';
else if(/^[/]track([/]|$)/.test(path))skip='track';
else if(/[?&#](code|state|token|access_token|id_token)=/i.test(q))skip='auth-url';
else if(!why)skip=/(^|; )dor_b=1(;|$)/.test(d.cookie||'')?'bounced':'no-trigger';
else if(last&&now-last<120000)skip='recent';
if(skip){v.ar='skip:'+skip;mark()}else reload(why);
}catch(e){}})();`
}

export default async function RootLayout({ children }: { children: React.ReactNode }) {
  const skin = skinOf(await getPublicSettings())
  const [veilBg, veilFg] = veilColorsOf(skin)
  return (
    <html lang="zh-TW" data-skin={skin !== 'default' ? skin : undefined}>
      <body><script dangerouslySetInnerHTML={{ __html: bootJs(veilBg, veilFg) }} /><AppProviders><BounceCleanup /><ViewportHeightFix /><ViewportDebug /><Analytics /><InAppBrowserNotice /><InterstitialAd /><PwaInstallPrompt /><UpdateNotice /><LandscapeNotice />{children}</AppProviders></body>
    </html>
  )
}
