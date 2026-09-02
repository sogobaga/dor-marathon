#!/usr/bin/env node
/**
 * vptest generator — 純靜態、零框架的 iOS Safari 視窗二分測試頁產生器。
 *
 * 用途：把 www.dor.tw 前台「App 化鎖版」(html,body{height:100%;overflow:hidden;
 * overscroll-behavior:none} + 根容器 height:max(100dvh,var(--app-h,0px))) 在 iOS Safari
 * 外部到站（掃 QR）時出現的「整頁往上位移約 100lvh-100svh、底部露黑帶」病態，拆成 10 個
 * 互相只差一個變因的靜態頁，方便用實機逐一隔離觸發條件。
 *
 * 用法：
 *   node scripts/vptest/gen.mjs           產生 10 個 variant + index.html，並自動跑驗證
 *   node scripts/vptest/gen.mjs --check   只驗證既有輸出（不重新產生）
 *
 * 只允許新增檔案：本檔＋它產出的 apps/web/public/vptest/*.html。不得改動 repo 其他檔案。
 */

import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const __dirname = path.dirname(fileURLToPath(import.meta.url))
const REPO_ROOT = path.resolve(__dirname, '..', '..')
const OUT_DIR = path.resolve(REPO_ROOT, 'apps', 'web', 'public', 'vptest')

// ---------------------------------------------------------------------------
// Variant 定義
// ---------------------------------------------------------------------------

const VIEWPORT_DEFAULT =
  'width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no, viewport-fit=cover'
const VIEWPORT_NOFIT =
  'width=device-width, initial-scale=1, maximum-scale=1, user-scalable=no'

// 標準「lock-osb」html,body 規則（多數 variant 共用的基底）
const HTML_BODY_LOCK = 'html,body{height:100%;overflow:hidden}'
const HTML_BODY_LOCK_OSB = 'html,body{height:100%;overflow:hidden;overscroll-behavior:none}'

// 標準「lock-osb」#root 高度規則（vh 起手 + dvh 支援時升級）
const ROOT_HEIGHT_LOCK_OSB = [
  '#root{height:100vh}',
  '@supports (height:100dvh){#root{height:100dvh}}',
].join('\n')

const VARIANTS = [
  {
    name: 'flow',
    desc: '對照組＝最初版本模型（純文件流，可捲動，無鎖版）',
    isFlow: true,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: '',
    extraHeadCss: '',
    rootCss: '#root{position:relative;background:#0c0e12;min-height:150vh}',
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: false,
  },
  {
    name: 'lock',
    desc: '07-01 鎖版模型：html/body 高度鎖 100% 並 overflow:hidden（尚未加 overscroll-behavior）',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: HTML_BODY_LOCK,
    extraHeadCss: '',
    rootCss: `#root{position:relative;overflow:hidden;background:#0c0e12}\n${ROOT_HEIGHT_LOCK_OSB}`,
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: false,
  },
  {
    name: 'lock-osb',
    desc: '07-28 模型（現行基礎）：lock 再加 overscroll-behavior:none',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: HTML_BODY_LOCK_OSB,
    extraHeadCss: '',
    rootCss: `#root{position:relative;overflow:hidden;background:#0c0e12}\n${ROOT_HEIGHT_LOCK_OSB}`,
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: false,
  },
  {
    name: 'lock-osb-vh',
    desc: '測「文件比可視區高 lvh−svh」：#root 只用 100vh（=lvh），不升級成 dvh',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: HTML_BODY_LOCK_OSB,
    extraHeadCss: '',
    rootCss: '#root{position:relative;overflow:hidden;background:#0c0e12;height:100vh}',
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: false,
  },
  {
    name: 'lock-osb-apph',
    desc: 'JS 量測後 --app-h 只准加高，html/body/#root 改用 max(現行值, var(--app-h))',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: HTML_BODY_LOCK_OSB,
    extraHeadCss:
      '@supports (height:max(0px,0px)){html,body{height:max(100%,var(--app-h,0px))}}',
    rootCss: [
      '#root{position:relative;overflow:hidden;background:#0c0e12}',
      '#root{height:100vh}',
      '@supports (height:max(0px,0px)){',
      '  #root{height:100dvh;height:max(100dvh,var(--app-h,0px))}',
      '}',
    ].join('\n'),
    rootInlineStyle: '',
    extraBodyScript: 'apph',
    slowBusyWait: false,
  },
  {
    name: 'lock-osb-overlay',
    desc: 'lock-osb ＋ 進站 1500ms 後彈出模擬蓋板廣告（測蓋板是否誘發位移）',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: `${HTML_BODY_LOCK_OSB}\nbody{background:#101318}`,
    extraHeadCss: '',
    rootCss: `#root{position:relative;overflow:hidden;background:#0c0e12}\n${ROOT_HEIGHT_LOCK_OSB}`,
    rootInlineStyle: '',
    extraBodyScript: 'overlay',
    slowBusyWait: false,
  },
  {
    name: 'lock-osb-slow',
    desc: 'lock-osb ＋ body 開頭同步忙等 1500ms，模擬「讀取暫停」後才繪製',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: HTML_BODY_LOCK_OSB,
    extraHeadCss: '',
    rootCss: `#root{position:relative;overflow:hidden;background:#0c0e12}\n${ROOT_HEIGHT_LOCK_OSB}`,
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: true,
  },
  {
    name: 'lock-osb-nofit',
    desc: 'lock-osb，但 viewport meta 拿掉 viewport-fit=cover',
    isFlow: false,
    viewport: VIEWPORT_NOFIT,
    htmlBodyCss: HTML_BODY_LOCK_OSB,
    extraHeadCss: '',
    rootCss: `#root{position:relative;overflow:hidden;background:#0c0e12}\n${ROOT_HEIGHT_LOCK_OSB}`,
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: false,
  },
  {
    name: 'lock-osb-transform',
    desc: 'lock-osb，#root 初始帶 transform:translateZ(0)，120ms 後移除（模擬桌機→手機切 class）',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: HTML_BODY_LOCK_OSB,
    extraHeadCss: '',
    rootCss: `#root{position:relative;overflow:hidden;background:#0c0e12}\n${ROOT_HEIGHT_LOCK_OSB}`,
    rootInlineStyle: 'transform:translateZ(0)',
    extraBodyScript: 'transform',
    slowBusyWait: false,
  },
  {
    name: 'lock-fixed',
    desc: '候選替代模型：body 不鎖，#root 改用 position:fixed;inset:0 撐滿',
    isFlow: false,
    viewport: VIEWPORT_DEFAULT,
    htmlBodyCss: '',
    extraHeadCss: '',
    rootCss: '#root{position:fixed;inset:0;height:auto;overflow:hidden;background:#0c0e12}',
    rootInlineStyle: '',
    extraBodyScript: null,
    slowBusyWait: false,
  },
]

// ---------------------------------------------------------------------------
// 共用 CSS / JS 字串模板
// ---------------------------------------------------------------------------

function buildStyle(v) {
  const posMode = v.isFlow ? 'fixed' : 'absolute'
  const lines = []
  lines.push('*{box-sizing:border-box}')
  lines.push(
    'html,body{margin:0;background:#0c0e12;color:#fff;font-family:-apple-system,system-ui,sans-serif}'
  )
  if (v.htmlBodyCss) lines.push(v.htmlBodyCss)
  if (v.extraHeadCss) lines.push(v.extraHeadCss)
  lines.push(v.rootCss)
  lines.push(
    `#top{position:${posMode};top:0;left:0;right:0;height:28px;background:#ffd60a;color:#000;font:700 13px/28px system-ui;text-align:center;z-index:10}`
  )
  lines.push(
    `#bottom{position:${posMode};bottom:0;left:0;right:0;height:28px;background:#30d158;color:#000;font:700 13px/28px system-ui;text-align:center;z-index:10}`
  )
  lines.push(
    `#hint{position:${posMode};top:34px;left:12px;right:12px;font:12px system-ui;color:#9aa3b2;z-index:10}`
  )
  lines.push(
    `#m{position:${posMode};top:36%;left:12px;right:12px;background:#1c1f26;border:1px solid #3a3f4b;border-radius:10px;padding:10px 12px;font:12px/1.5 ui-monospace,Menlo,monospace;white-space:pre-wrap;z-index:10}`
  )
  lines.push('.ruler-line{position:absolute;left:0;right:0;height:1px;background:rgba(255,255,255,.35)}')
  lines.push('.ruler-label{position:absolute;left:4px;top:-13px;font:11px system-ui;color:rgba(255,255,255,.7)}')
  lines.push('#m .btnrow{display:flex;gap:8px;flex-wrap:wrap;margin-top:8px}')
  lines.push('#m button{font:600 13px system-ui;padding:8px 10px;border-radius:8px;border:0;background:#2f6fed;color:#fff}')
  return lines.join('\n')
}

// <head> 內第一個同步 script：t=0 立刻取 innerHeight
function buildHeadScript() {
  return ['<script>', 'var __ihms = {};', '__ihms[0] = window.innerHeight;', '<\/script>'].join('\n')
}

// body 開頭同步忙等（僅 lock-osb-slow 使用），必須是 body 內第一個元素
function buildSlowScript() {
  return [
    '<script>',
    '(function(){',
    '  var t0 = Date.now();',
    '  while (Date.now() - t0 < 1500) {}',
    '})();',
    '<\/script>',
  ].join('\n')
}

// 主量測面板／尺規／按鈕 script（每個 variant 都有，共用邏輯，僅 name/desc 不同）
function buildMainScript(v) {
  const nameLit = JSON.stringify(v.name)
  const descLit = JSON.stringify(v.desc)
  return [
    '<script>',
    '(function(){',
    "  'use strict';",
    '  function $(id){ return document.getElementById(id); }',
    '',
    '  // 尺規：50px 一條，畫到 1100px',
    '  var rootEl = $("root");',
    '  (function buildRuler(){',
    '    var step = 50, max = 1100;',
    '    for (var t = step; t <= max; t += step) {',
    '      var line = document.createElement("div");',
    '      line.className = "ruler-line";',
    '      line.style.top = t + "px";',
    '      var label = document.createElement("span");',
    '      label.className = "ruler-label";',
    '      label.textContent = t + "px";',
    '      line.appendChild(label);',
    '      rootEl.appendChild(line);',
    '    }',
    '  })();',
    '',
    '  // ih@ms：0/100/500/1000/2000/3000 各時間點的 innerHeight',
    '  var MS_POINTS = [100, 500, 1000, 2000, 3000];',
    '  window.__ihms = window.__ihms || {};',
    '  for (var i = 0; i < MS_POINTS.length; i++) {',
    '    (function(ms){',
    '      setTimeout(function(){',
    '        try { window.__ihms[ms] = window.innerHeight; } catch (e) {}',
    '      }, ms);',
    '    })(MS_POINTS[i]);',
    '  }',
    '',
    '  // 觸點越界偵測：capture 監聽 pointerdown',
    '  window.__lastTap = null;',
    '  try {',
    '    document.addEventListener("pointerdown", function(e){',
    '      window.__lastTap = { y: e.clientY };',
    '    }, true);',
    '  } catch (e) {}',
    '',
    '  function probeUnit(unit, id){',
    '    try {',
    '      if (window.CSS && CSS.supports && CSS.supports("height", "100" + unit)) {',
    '        var el = $(id);',
    '        if (el) return el.offsetHeight;',
    '      }',
    '    } catch (e) {}',
    '    return "-";',
    '  }',
    '',
    '  function collectData(){',
    '    var data = {',
    '      variant: ' + nameLit + ',',
    '      desc: ' + descLit + ',',
    '      ih: window.innerHeight,',
    '      ch: document.documentElement.clientHeight,',
    '      sh: (window.screen && screen.height) || null,',
    '      sy: window.scrollY,',
    '      vv: null,',
    '      units: {',
    '        vh: probeUnit("vh", "probe-vh"),',
    '        svh: probeUnit("svh", "probe-svh"),',
    '        lvh: probeUnit("lvh", "probe-lvh"),',
    '        dvh: probeUnit("dvh", "probe-dvh")',
    '      },',
    '      appH: "-",',
    '      nav: "?",',
    '      ref: document.referrer || "(none)",',
    '      ihms: {},',
    '      tap: null,',
    '      ua: (navigator.userAgent || "").slice(0, 90)',
    '    };',
    '    try {',
    '      if (window.visualViewport) {',
    '        var vv = window.visualViewport;',
    '        data.vv = { h: vv.height, ot: vv.offsetTop, pt: vv.pageTop, sc: vv.scale };',
    '      }',
    '    } catch (e) {}',
    '    try {',
    '      data.appH = document.documentElement.style.getPropertyValue("--app-h") || "-";',
    '    } catch (e) {}',
    '    try {',
    '      if (window.performance && performance.getEntriesByType) {',
    '        var entries = performance.getEntriesByType("navigation");',
    '        if (entries && entries[0]) data.nav = entries[0].type;',
    '      }',
    '    } catch (e) {}',
    '    var pts = [0, 100, 500, 1000, 2000, 3000];',
    '    for (var i = 0; i < pts.length; i++) {',
    '      var v = window.__ihms ? window.__ihms[pts[i]] : undefined;',
    '      data.ihms[pts[i]] = (v === undefined) ? null : v;',
    '    }',
    '    var chNow = data.ch;',
    '    if (window.__lastTap) {',
    '      var y = window.__lastTap.y;',
    '      data.tap = { y: y, ch: chNow, outOfBounds: (y > chNow + 8) };',
    '    }',
    '    return data;',
    '  }',
    '',
    '  function fmtVV(data){',
    '    if (!data.vv) return "vv n/a";',
    '    return "vv h=" + data.vv.h + " ot=" + data.vv.ot + " pt=" + data.vv.pt + " sc=" + data.vv.sc;',
    '  }',
    '',
    '  function fmtIhMs(data){',
    '    var pts = [0, 100, 500, 1000, 2000, 3000];',
    '    var out = [];',
    '    for (var i = 0; i < pts.length; i++) {',
    '      var v = data.ihms[pts[i]];',
    '      out.push(pts[i] + "\\u2192" + (v === null ? "\\u2026" : v));',
    '    }',
    '    return "ih@ms: " + out.join(" ");',
    '  }',
    '',
    '  function fmtTap(data){',
    '    if (!data.tap) return "tap: -";',
    '    var status = data.tap.outOfBounds ? "\\u26a0\\ufe0f\\u8d8a\\u754c" : "ok";',
    '    return "tap: y=" + data.tap.y + " ch=" + data.tap.ch + " " + status;',
    '  }',
    '',
    '  function buildPanelText(data){',
    '    var lines = [];',
    '    lines.push("variant: " + data.variant);',
    '    lines.push(data.desc);',
    '    lines.push("ih=" + data.ih + " ch=" + data.ch + " sh=" + data.sh + " sy=" + data.sy);',
    '    lines.push(fmtVV(data));',
    '    lines.push("vh=" + data.units.vh + " svh=" + data.units.svh + " lvh=" + data.units.lvh + " dvh=" + data.units.dvh);',
    '    lines.push("--app-h=" + data.appH);',
    '    lines.push("nav=" + data.nav + " ref=" + data.ref);',
    '    lines.push(fmtIhMs(data));',
    '    lines.push(fmtTap(data));',
    '    lines.push("ua: " + data.ua);',
    '    return lines.join("\\n");',
    '  }',
    '',
    '  function btnScrollTop(){',
    '    window.scrollTo(0, 0);',
    '    document.documentElement.scrollTop = 0;',
    '    document.body.scrollTop = 0;',
    '  }',
    '',
    '  function btnOpenScrollClose(){',
    '    var html = document.documentElement, body = document.body, root = $("root");',
    '    var orig = {',
    '      ho: html.style.overflow, hb: html.style.overscrollBehavior,',
    '      bo: body.style.overflow, bb: body.style.overscrollBehavior,',
    '      rm: root.style.minHeight',
    '    };',
    '    html.style.overflow = "auto";',
    '    html.style.overscrollBehavior = "auto";',
    '    body.style.overflow = "auto";',
    '    body.style.overscrollBehavior = "auto";',
    '    root.style.minHeight = "calc(100% + 2px)";',
    '    window.scrollTo(0, 1);',
    '    requestAnimationFrame(function(){',
    '      window.scrollTo(0, 0);',
    '      requestAnimationFrame(function(){',
    '        html.style.overflow = orig.ho;',
    '        html.style.overscrollBehavior = orig.hb;',
    '        body.style.overflow = orig.bo;',
    '        body.style.overscrollBehavior = orig.bb;',
    '        root.style.minHeight = orig.rm;',
    '      });',
    '    });',
    '  }',
    '',
    '  function btnCopyDiag(btnEl){',
    '    var data = collectData();',
    '    var original = btnEl.textContent;',
    '    try {',
    '      if (navigator.clipboard && navigator.clipboard.writeText) {',
    '        navigator.clipboard.writeText(JSON.stringify(data)).then(function(){',
    '          btnEl.textContent = "\\u5df2\\u8907\\u88fd";',
    '          setTimeout(function(){ btnEl.textContent = original; }, 1500);',
    '        })["catch"](function(){});',
    '      }',
    '    } catch (e) {}',
    '  }',
    '',
    '  function buildButtons(container){',
    '    var b1 = document.createElement("button");',
    '    b1.type = "button";',
    '    b1.textContent = "\\ud83d\\udd04 reload";',
    '    b1.addEventListener("click", function(){ location.reload(); });',
    '',
    '    var b2 = document.createElement("button");',
    '    b2.type = "button";',
    '    b2.textContent = "\\u2460 scrollTo(0,0)";',
    '    b2.addEventListener("click", btnScrollTop);',
    '',
    '    var b3 = document.createElement("button");',
    '    b3.type = "button";',
    '    b3.textContent = "\\u2461 \\u958b\\u6372\\u2192\\u63771px\\u2192\\u95dc";',
    '    b3.addEventListener("click", btnOpenScrollClose);',
    '',
    '    var b4 = document.createElement("button");',
    '    b4.type = "button";',
    '    b4.textContent = "\\u2462 \\u8907\\u88fd\\u8a3a\\u65b7";',
    '    b4.addEventListener("click", function(){ btnCopyDiag(b4); });',
    '',
    '    container.appendChild(b1);',
    '    container.appendChild(b2);',
    '    container.appendChild(b3);',
    '    container.appendChild(b4);',
    '  }',
    '',
    '  var mText = null;',
    '  function renderPanel(){',
    '    var m = $("m");',
    '    if (!m) return;',
    '    if (!mText) {',
    '      m.textContent = "";',
    '      mText = document.createElement("div");',
    '      m.appendChild(mText);',
    '      var row = document.createElement("div");',
    '      row.className = "btnrow";',
    '      m.appendChild(row);',
    '      buildButtons(row);',
    '    }',
    '    mText.textContent = buildPanelText(collectData());',
    '  }',
    '',
    '  renderPanel();',
    '  setInterval(renderPanel, 500);',
    '})();',
    '<\/script>',
  ].join('\n')
}

// variant 專屬額外 script（apph / overlay / transform），各自獨立自足
function buildExtraScript(kind) {
  if (kind === 'apph') {
    return [
      '<script>',
      '(function(){',
      '  function measure(){',
      '    try {',
      '      var h = window.innerHeight;',
      '      if (window.visualViewport && window.visualViewport.height > h) h = window.visualViewport.height;',
      '      var cur = document.documentElement.style.getPropertyValue("--app-h");',
      '      var curPx = cur ? parseFloat(cur) : 0;',
      '      if (h > curPx) {',
      '        document.documentElement.style.setProperty("--app-h", h + "px");',
      '      }',
      '    } catch (e) {}',
      '  }',
      '  function ready(fn){',
      '    if (document.readyState === "loading") {',
      '      document.addEventListener("DOMContentLoaded", fn);',
      '    } else {',
      '      fn();',
      '    }',
      '  }',
      '  ready(function(){ setTimeout(measure, 50); });',
      '  try {',
      '    window.addEventListener("pageshow", measure);',
      '    document.addEventListener("visibilitychange", measure);',
      '    window.addEventListener("resize", measure);',
      '    window.addEventListener("focus", measure);',
      '    if (window.visualViewport) {',
      '      window.visualViewport.addEventListener("resize", measure);',
      '    }',
      '  } catch (e) {}',
      '  setInterval(measure, 1500);',
      '})();',
      '<\/script>',
    ].join('\n')
  }
  if (kind === 'overlay') {
    return [
      '<script>',
      '(function(){',
      '  function mountOverlay(){',
      '    var html = document.documentElement, body = document.body;',
      '    var origHtmlBg = html.style.background;',
      '    var origBodyBg = body.style.background;',
      '    html.style.background = "#0c0e12";',
      '    body.style.background = "#0c0e12";',
      '',
      '    var overlay = document.createElement("div");',
      '    overlay.id = "sim-ad-overlay";',
      '    overlay.style.position = "fixed";',
      '    overlay.style.inset = "0";',
      '    overlay.style.height = "100dvh";',
      '    overlay.style.zIndex = "2500";',
      '    overlay.style.background = "rgba(0,0,0,.6)";',
      '    overlay.style.display = "flex";',
      '    overlay.style.alignItems = "center";',
      '    overlay.style.justifyContent = "center";',
      '',
      '    var card = document.createElement("div");',
      '    card.style.width = "78%";',
      '    card.style.aspectRatio = "3/4";',
      '    card.style.background = "#fff";',
      '    card.style.borderRadius = "14px";',
      '    card.style.willChange = "transform";',
      '    card.style.color = "#000";',
      '    card.style.display = "flex";',
      '    card.style.alignItems = "center";',
      '    card.style.justifyContent = "center";',
      '    card.style.textAlign = "center";',
      '    card.style.padding = "16px";',
      '    card.textContent = "\\u6a21\\u64ec\\u84cb\\u677f\\u5ee3\\u544a\\uff08\\u9ede\\u4efb\\u610f\\u8655\\u95dc\\u9589\\uff09";',
      '',
      '    overlay.appendChild(card);',
      '    overlay.addEventListener("click", function(){',
      '      if (overlay.parentNode) overlay.parentNode.removeChild(overlay);',
      '      html.style.background = origHtmlBg;',
      '      body.style.background = origBodyBg;',
      '    });',
      '    document.body.appendChild(overlay);',
      '  }',
      '  function afterLoad(fn){',
      '    if (document.readyState === "complete") { fn(); }',
      '    else { window.addEventListener("load", fn); }',
      '  }',
      '  afterLoad(function(){ setTimeout(mountOverlay, 1500); });',
      '})();',
      '<\/script>',
    ].join('\n')
  }
  if (kind === 'transform') {
    return [
      '<script>',
      '(function(){',
      '  function ready(fn){',
      '    if (document.readyState === "loading") {',
      '      document.addEventListener("DOMContentLoaded", fn);',
      '    } else {',
      '      fn();',
      '    }',
      '  }',
      '  ready(function(){',
      '    setTimeout(function(){',
      '      var root = document.getElementById("root");',
      '      if (root) root.style.transform = "";',
      '    }, 120);',
      '  });',
      '})();',
      '<\/script>',
    ].join('\n')
  }
  return ''
}

function buildProbes() {
  return [
    '<div id="probe-vh" style="position:fixed;left:-9999px;width:1px;visibility:hidden;height:100vh"></div>',
    '<div id="probe-svh" style="position:fixed;left:-9999px;width:1px;visibility:hidden;height:100svh"></div>',
    '<div id="probe-lvh" style="position:fixed;left:-9999px;width:1px;visibility:hidden;height:100lvh"></div>',
    '<div id="probe-dvh" style="position:fixed;left:-9999px;width:1px;visibility:hidden;height:100dvh"></div>',
  ].join('\n')
}

function buildPage(v) {
  const rootAttr = v.rootInlineStyle ? ` style="${v.rootInlineStyle}"` : ''
  const parts = []
  parts.push('<!DOCTYPE html>')
  parts.push('<html lang="zh-TW">')
  parts.push('<head>')
  parts.push('<meta charset="utf-8">')
  parts.push(`<meta name="viewport" content="${v.viewport}">`)
  parts.push('<meta name="theme-color" content="#0c0e12">')
  parts.push(`<title>vptest: ${v.name}</title>`)
  parts.push(buildHeadScript())
  parts.push('<style>')
  parts.push(buildStyle(v))
  parts.push('</style>')
  parts.push('</head>')
  parts.push('<body>')
  if (v.slowBusyWait) parts.push(buildSlowScript())
  parts.push(`<div id="root"${rootAttr}>`)
  parts.push('<div id="top">▲ 頂端 TOP ▲　看不到這條黃色＝內容被往上推</div>')
  parts.push('<div id="bottom">▼ 底端 BOTTOM ▼　這條綠色下方若有黑帶＝跑版</div>')
  parts.push('<div id="hint">用相機掃 QR 進來看：黃條在最上、綠條貼底且下方無黑帶＝正常</div>')
  parts.push('<div id="m">量測中…</div>')
  parts.push('</div>')
  parts.push(buildProbes())
  parts.push(buildMainScript(v))
  const extra = buildExtraScript(v.extraBodyScript)
  if (extra) parts.push(extra)
  parts.push('</body>')
  parts.push('</html>')
  return parts.join('\n') + '\n'
}

function buildIndex(variants) {
  const parts = []
  parts.push('<!DOCTYPE html>')
  parts.push('<html lang="zh-TW">')
  parts.push('<head>')
  parts.push('<meta charset="utf-8">')
  parts.push(`<meta name="viewport" content="${VIEWPORT_DEFAULT}">`)
  parts.push('<meta name="theme-color" content="#0c0e12">')
  parts.push('<title>vptest: index</title>')
  parts.push('<style>')
  parts.push('*{box-sizing:border-box}')
  parts.push('html,body{margin:0;background:#0c0e12;color:#fff;font-family:-apple-system,system-ui,sans-serif;padding:20px}')
  parts.push('h1{font-size:18px}')
  parts.push('#warn{background:#3a2a00;border:1px solid #ffd60a;color:#ffd60a;border-radius:10px;padding:10px 12px;font:13px/1.6 system-ui;margin-bottom:16px}')
  parts.push('ul{list-style:none;padding:0;margin:0}')
  parts.push('li{margin-bottom:10px;background:#1c1f26;border:1px solid #3a3f4b;border-radius:10px;padding:10px 12px}')
  parts.push('a{color:#7fb1ff;font:600 15px system-ui;text-decoration:none}')
  parts.push('a:active{opacity:.7}')
  parts.push('p{margin:4px 0 0;font:12px/1.5 system-ui;color:#9aa3b2}')
  parts.push('</style>')
  parts.push('</head>')
  parts.push('<body>')
  parts.push('<h1>vptest — iOS Safari 視窗二分測試頁</h1>')
  parts.push(
    '<div id="warn">⚠️ 從這裡點連結是站內跳轉，不等於掃 QR 的外部到站；正式測試請用相機掃 QR。</div>'
  )
  parts.push('<ul>')
  for (const v of variants) {
    parts.push('<li>')
    parts.push(`<a href="/vptest/${v.name}.html">${v.name}.html</a>`)
    parts.push(`<p>${v.desc}</p>`)
    parts.push('</li>')
  }
  parts.push('</ul>')
  parts.push('</body>')
  parts.push('</html>')
  return parts.join('\n') + '\n'
}

// ---------------------------------------------------------------------------
// 產生
// ---------------------------------------------------------------------------

function generate() {
  fs.mkdirSync(OUT_DIR, { recursive: true })
  const written = []
  for (const v of VARIANTS) {
    const html = buildPage(v)
    const file = path.join(OUT_DIR, `${v.name}.html`)
    fs.writeFileSync(file, html, 'utf8')
    written.push(file)
  }
  const indexHtml = buildIndex(VARIANTS)
  const indexFile = path.join(OUT_DIR, 'index.html')
  fs.writeFileSync(indexFile, indexHtml, 'utf8')
  written.push(indexFile)
  return written
}

// ---------------------------------------------------------------------------
// 驗證（--check）
// ---------------------------------------------------------------------------

function extractStyleBlock(html) {
  const m = html.match(/<style>([\s\S]*?)<\/style>/)
  return m ? m[1] : ''
}

function countOcc(str, sub) {
  if (!sub) return 0
  let n = 0
  let idx = 0
  while ((idx = str.indexOf(sub, idx)) !== -1) {
    n++
    idx += sub.length
  }
  return n
}

function checkFile(results, file, label, checks) {
  const exists = fs.existsSync(file)
  results.push({ label: `${label}: 檔案存在`, pass: exists, detail: file })
  if (!exists) return null
  const content = fs.readFileSync(file, 'utf8')
  for (const c of checks) {
    let pass
    try {
      pass = c.fn(content)
    } catch (e) {
      pass = false
    }
    results.push({ label: `${label}: ${c.name}`, pass: !!pass, detail: c.detail || '' })
  }
  return content
}

function structuralChecks(content) {
  const openCount = countOcc(content.toLowerCase(), '<script')
  const closeCount = countOcc(content.toLowerCase(), '</script>')
  const scriptTagsBalanced = openCount === closeCount && openCount > 0
  // 除了真正的 </script> 收尾標籤外，不應該再出現裸的 "</script" 字樣
  const closeLoose = countOcc(content.toLowerCase(), '</script')
  const noStrayCloseTag = closeLoose === closeCount
  const noBackslashArtifact = !content.includes('<\\/script>')
  return { scriptTagsBalanced, noStrayCloseTag, noBackslashArtifact, openCount, closeCount }
}

function runChecks() {
  const results = []

  for (const v of VARIANTS) {
    const file = path.join(OUT_DIR, `${v.name}.html`)
    const label = v.name
    const checks = []

    checks.push({
      name: 'viewport meta 內容正確',
      fn: (c) => c.includes(`content="${v.viewport}"`),
    })
    if (v.name === 'lock-osb-nofit') {
      checks.push({
        name: 'viewport meta 不含 viewport-fit=cover',
        fn: (c) => {
          const m = c.match(/<meta name="viewport" content="([^"]*)">/)
          return !!m && !m[1].includes('viewport-fit=cover')
        },
      })
    } else {
      checks.push({
        name: 'viewport meta 含 viewport-fit=cover',
        fn: (c) => {
          const m = c.match(/<meta name="viewport" content="([^"]*)">/)
          return !!m && m[1].includes('viewport-fit=cover')
        },
      })
    }

    checks.push({
      name: 'title 正確',
      fn: (c) => c.includes(`<title>vptest: ${v.name}</title>`),
    })

    checks.push({
      name: '結構：script 標籤成對、無裸 </script 字樣',
      fn: (c) => {
        const s = structuralChecks(c)
        return s.scriptTagsBalanced && s.noStrayCloseTag && s.noBackslashArtifact
      },
    })

    checks.push({ name: '含 #top/#bottom/#m/#hint', fn: (c) => ['id="top"', 'id="bottom"', 'id="m"', 'id="hint"'].every((s) => c.includes(s)) })
    checks.push({ name: '含 4 個量測探針 div', fn: (c) => ['probe-vh', 'probe-svh', 'probe-lvh', 'probe-dvh'].every((s) => c.includes(s)) })

    // CSS 片段檢查（限定在 <style> 區塊內，避免探針 inline style 的 dvh 誤判）
    const cssChecks = []
    if (v.name === 'flow') {
      cssChecks.push({ name: 'CSS: 無 html,body overflow:hidden 鎖版', fn: (s) => !s.includes('html,body{height:100%;overflow:hidden') })
      cssChecks.push({ name: 'CSS: #root min-height:150vh', fn: (s) => s.includes('min-height:150vh') })
    } else if (v.name === 'lock-fixed') {
      cssChecks.push({ name: 'CSS: 無 html,body overflow:hidden 鎖版', fn: (s) => !s.includes('html,body{height:100%;overflow:hidden') })
      cssChecks.push({ name: 'CSS: #root position:fixed;inset:0', fn: (s) => s.includes('#root{position:fixed;inset:0') })
      cssChecks.push({ name: 'CSS: 不含 overscroll-behavior:none', fn: (s) => !s.includes('overscroll-behavior:none') })
    } else {
      cssChecks.push({ name: 'CSS: html,body height:100%;overflow:hidden', fn: (s) => s.includes('height:100%;overflow:hidden') })
      if (v.name === 'lock') {
        cssChecks.push({ name: 'CSS: 不含 overscroll-behavior:none', fn: (s) => !s.includes('overscroll-behavior:none') })
      } else {
        cssChecks.push({ name: 'CSS: 含 overscroll-behavior:none', fn: (s) => s.includes('overscroll-behavior:none') })
      }
      if (v.name === 'lock-osb-vh') {
        cssChecks.push({ name: 'CSS: #root 只有 height:100vh（不含 dvh）', fn: (s) => s.includes('#root{position:relative;overflow:hidden;background:#0c0e12;height:100vh}') && !s.includes('dvh') })
      } else {
        cssChecks.push({ name: 'CSS: 含 100dvh 升級規則', fn: (s) => s.includes('100dvh') })
      }
      if (v.name === 'lock-osb-apph') {
        cssChecks.push({ name: 'CSS: 含 var(--app-h', fn: (s) => s.includes('var(--app-h') })
      }
      if (v.name === 'lock-osb-overlay') {
        cssChecks.push({ name: 'CSS: body 初始背景 #101318', fn: (s) => s.includes('body{background:#101318}') })
      }
    }
    checks.push({
      name: 'CSS 片段組合',
      fn: (c) => {
        const style = extractStyleBlock(c)
        return cssChecks.every((cc) => cc.fn(style))
      },
      detail: cssChecks.map((cc) => cc.name).join(' / '),
    })

    // variant 專屬 body 內容檢查
    if (v.name === 'lock-osb-slow') {
      checks.push({ name: 'body 開頭同步忙等 script（在 #root 之前）', fn: (c) => {
        const bodyIdx = c.indexOf('<body>')
        const busyIdx = c.indexOf('Date.now() - t0 < 1500')
        const rootIdx = c.indexOf('<div id="root"')
        return bodyIdx !== -1 && busyIdx !== -1 && rootIdx !== -1 && bodyIdx < busyIdx && busyIdx < rootIdx
      }})
    } else {
      checks.push({ name: '不含忙等 script', fn: (c) => !c.includes('Date.now() - t0 < 1500') })
    }

    if (v.name === 'lock-osb-transform') {
      checks.push({ name: '#root 帶 inline transform:translateZ(0)', fn: (c) => c.includes('id="root" style="transform:translateZ(0)"') })
      checks.push({ name: '含 120ms 移除 transform 邏輯', fn: (c) => c.includes('root.style.transform = "";') && c.includes('}, 120);') })
    } else {
      checks.push({ name: '不含 transform:translateZ(0) inline', fn: (c) => !c.includes('style="transform:translateZ(0)"') })
    }

    if (v.name === 'lock-osb-apph') {
      checks.push({ name: '含 measure()/--app-h 只加高邏輯', fn: (c) => c.includes('setProperty("--app-h"') && c.includes('if (h > curPx)') })
    } else {
      checks.push({ name: '不含 apph measure 邏輯', fn: (c) => !c.includes('setProperty("--app-h"') })
    }

    if (v.name === 'lock-osb-overlay') {
      checks.push({ name: '含模擬蓋板廣告 overlay 邏輯', fn: (c) => c.includes('mountOverlay') && c.includes('setTimeout(mountOverlay, 1500)') })
    } else {
      checks.push({ name: '不含 overlay 邏輯', fn: (c) => !c.includes('mountOverlay') })
    }

    checks.push({ name: '含面板/按鈕/尺規主 script', fn: (c) => c.includes('buildPanelText') && c.includes('buildButtons') && c.includes('buildRuler') })

    checkFile(results, file, label, checks)
  }

  // index.html
  const indexFile = path.join(OUT_DIR, 'index.html')
  checkFile(results, indexFile, 'index', [
    { name: '含警語（站內跳轉≠掃QR）', fn: (c) => c.includes('不等於掃 QR 的外部到站') },
    { name: '含 10 個 variant 連結', fn: (c) => VARIANTS.every((v) => c.includes(`/vptest/${v.name}.html`)) },
    { name: '結構：script 標籤成對（index 無 script 亦可）', fn: (c) => {
      const s = structuralChecks(c)
      return s.openCount === 0 ? true : (s.scriptTagsBalanced && s.noStrayCloseTag)
    } },
  ])

  const pass = results.filter((r) => r.pass).length
  const fail = results.filter((r) => !r.pass)
  console.log(`\n=== vptest 驗證結果：${pass}/${results.length} PASS ===\n`)
  for (const r of results) {
    console.log(`${r.pass ? 'PASS' : 'FAIL'}  ${r.label}${r.detail ? '  (' + r.detail + ')' : ''}`)
  }
  if (fail.length > 0) {
    console.log(`\n${fail.length} 項 FAIL`)
    process.exitCode = 1
  } else {
    console.log('\n全部通過')
  }
}

// ---------------------------------------------------------------------------
// entrypoint
// ---------------------------------------------------------------------------

const args = process.argv.slice(2)
if (args.includes('--check')) {
  runChecks()
} else {
  const written = generate()
  console.log(`已產生 ${written.length} 個檔案於 ${OUT_DIR}`)
  for (const f of written) {
    const size = fs.statSync(f).size
    console.log(`  ${path.relative(REPO_ROOT, f)}  (${size} bytes)`)
  }
  runChecks()
}
