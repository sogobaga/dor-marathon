// DOR｜城市探索 宣傳片算圖腳本
// 用法：node render.mjs [--fps 30] [--from 0] [--to 44] [--out dor-city-explore-promo.mp4] [--still 12,20]
// 需求：playwright（全域或本機）、ffmpeg（PATH 或 FFMPEG 環境變數）、python3（產生配樂）
import { createRequire } from 'node:module';
import { spawn, execSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));
const arg = (k, d) => { const i = process.argv.indexOf(`--${k}`); return i > 0 ? process.argv[i + 1] : d; };
const fps = +arg('fps', 30);
const out = join(here, arg('out', 'dor-city-explore-promo.mp4'));
const ffmpeg = process.env.FFMPEG || 'ffmpeg';

const require = createRequire(import.meta.url);
let playwright;
try { playwright = require('playwright'); }
catch { playwright = require(join(execSync('npm root -g').toString().trim(), 'playwright')); }

// 1) 字型：Google Fonts 下載到 fonts/（CJK 字型切片多，不進 git）
if (!existsSync(join(here, 'fonts/local.css'))) {
  console.log('下載字型…');
  execSync(`python3 ${join(here, 'fetch_fonts.py')}`, { stdio: 'inherit', cwd: here });
}

const browser = await playwright.chromium.launch();
const page = await browser.newPage({ viewport: { width: 1920, height: 1080 }, deviceScaleFactor: 1 });
await page.goto('file://' + join(here, 'index.html'));
await page.evaluate(async () => {
  await document.fonts.ready;
  await Promise.all([...document.images].map(i => i.decode().catch(() => {})));
});
const duration = await page.evaluate(() => window.DURATION);

// 預覽單張：--still 12,20 → still-12.png …
const still = arg('still');
if (still) {
  for (const t of still.split(',').map(Number)) {
    await page.evaluate(t => window.seek(t), t);
    await page.screenshot({ path: join(here, `still-${t}.png`) });
    console.log('still', t);
  }
  await browser.close();
  process.exit(0);
}

const from = +arg('from', 0), to = +arg('to', duration);

// 2) 配樂
const wav = join(here, 'music.wav');
execSync(`python3 ${join(here, 'music.py')} ${wav} ${duration}`, { stdio: 'inherit' });

// 3) 逐格截圖 → ffmpeg
const ff = spawn(ffmpeg, [
  '-y', '-loglevel', 'error',
  '-f', 'image2pipe', '-framerate', String(fps), '-c:v', 'mjpeg', '-i', '-',
  '-ss', String(from), '-i', wav,
  '-c:v', 'libx264', '-preset', 'slow', '-crf', '18', '-pix_fmt', 'yuv420p', '-movflags', '+faststart',
  '-c:a', 'aac', '-b:a', '192k', '-shortest', out,
], { stdio: ['pipe', 'inherit', 'inherit'] });

const total = Math.round((to - from) * fps);
for (let f = 0; f < total; f++) {
  const t = from + f / fps;
  await page.evaluate(t => window.seek(t), t);
  const buf = await page.screenshot({ type: 'jpeg', quality: 95 });
  if (!ff.stdin.write(buf)) await new Promise(r => ff.stdin.once('drain', r));
  if (f % fps === 0) process.stdout.write(`\r${t.toFixed(0)}s / ${to}s`);
}
ff.stdin.end();
await new Promise(r => ff.on('close', r));
await browser.close();
console.log(`\n完成：${out}`);
