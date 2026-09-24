"""DOR 城市探索宣傳片配樂：純 Python 合成（無外部相依），120 BPM、C–G–Am–F 進行。
用法：python3 music.py out.wav 44
"""
import math, random, struct, sys, wave

SR = 44100
OUT = sys.argv[1] if len(sys.argv) > 1 else 'music.wav'
DUR = float(sys.argv[2]) if len(sys.argv) > 2 else 44
N = int(SR * DUR)
L = [0.0] * N
R = [0.0] * N
BEAT = 0.5
# 分段時間點（與 index.html 的場景切換一致）
LOGO, ARP, END = 6.0, 11.0, 42.2
TRANSITIONS = (6, 11, 18.4, 24.6, 31.4, 37, 42.2)

random.seed(7)

def hz(m):
    return 440 * 2 ** ((m - 69) / 12)

def put(t0, samples, gain=1.0, pan=0.0):
    i0 = int(t0 * SR)
    gl, gr = gain * (1 - max(pan, 0)), gain * (1 + min(pan, 0))
    for k, s in enumerate(samples):
        i = i0 + k
        if 0 <= i < N:
            L[i] += s * gl
            R[i] += s * gr

def kick(amp=1.0):
    ph, out = 0.0, []
    for k in range(int(.35 * SR)):
        t = k / SR
        ph += 2 * math.pi * (45 + 110 * math.exp(-t * 28)) / SR
        out.append(math.sin(ph) * math.exp(-t * 9) * amp)
    return out

def noise(dur, decay, lp=0.5, hp=False):
    out, y, prev = [], 0.0, 0.0
    for k in range(int(dur * SR)):
        x = random.uniform(-1, 1)
        y += lp * (x - y)
        s = (y - prev) if hp else y
        prev = y
        out.append(s * math.exp(-k / SR * decay))
    return out

def tone(f, dur, attack=.005, decay=6.0, shape='tri', harm=None):
    out, n = [], int(dur * SR)
    for k in range(n):
        t = k / SR
        p = (f * t) % 1.0
        if shape == 'tri':
            s = 4 * abs(p - .5) - 1
        elif shape == 'saw':
            s = sum(math.sin(2 * math.pi * f * h * t) / h for h in range(1, 6)) * .6
        else:
            s = math.sin(2 * math.pi * f * t)
        env = min(1, t / attack) * math.exp(-t * decay)
        out.append(s * env)
    return out

def pad(notes, dur):
    out, n = [], int(dur * SR)
    fs = [hz(m) * d for m in notes for d in (0.997, 1.003)]
    for k in range(n):
        t = k / SR
        env = min(1, t / .6) * min(1, (dur - t) / .4)
        out.append(sum(math.sin(2 * math.pi * f * t) for f in fs) / len(fs) * env)
    return out

# C – G – Am – F（每和弦 2 秒 = 4 拍）
CHORDS = [(48, [60, 64, 67]), (43, [59, 62, 67]), (45, [60, 64, 69]), (41, [60, 65, 69])]
def chord_at(t):
    return CHORDS[int(t / 2) % 4]

# 開場氛圍 pad（整段）
for c in range(int(DUR / 2)):
    t0 = c * 2
    root, notes = chord_at(t0)
    put(t0, pad(notes, 2.05), .13 if t0 < LOGO or t0 >= END else .08)

# Logo 前 riser
put(LOGO - 3, [s * (k / (3 * SR)) ** 2 for k, s in enumerate(noise(3, 0, .25))], .25)
# Logo 登場衝擊
put(LOGO, kick(1.3), .9)
put(LOGO, noise(1.6, 2.5, .15), .35)
put(LOGO, tone(hz(36), 2.5, .01, 1.5, 'sine'), .5)

# 轉場 whoosh
for tr in TRANSITIONS:
    w = noise(.6, 0, .35)
    put(tr - .45, [s * math.sin(math.pi * k / len(w)) for k, s in enumerate(w)], .22, random.choice((-.4, .4)))

# 節奏段 LOGO–END
t = LOGO
while t < END - 1e-6:
    beat = round((t - LOGO) / BEAT)
    root, notes = chord_at(t)
    put(t, kick(), .85)
    if t >= ARP and beat % 2 == 1:
        put(t, noise(.18, 22, .6), .28)                       # clap
    put(t + BEAT / 2, noise(.05, 90, .9, hp=True), .22, .3)   # 反拍 hi-hat
    if t >= ARP:
        put(t + BEAT / 4, noise(.03, 140, .9, hp=True), .1, -.3)
        put(t + 3 * BEAT / 4, noise(.03, 140, .9, hp=True), .1, -.3)
    # 貝斯：八分音符
    for e in range(2):
        put(t + e * BEAT / 2, tone(hz(root), .24, .005, 9, 'saw'), .22)
    # 琶音：十六分音符（S3 起）
    if t >= ARP:
        arp = notes + [notes[0] + 12]
        for s in range(4):
            put(t + s * BEAT / 4, tone(hz(arp[(beat * 4 + s) % 4] + 12), .2, .003, 14), .09, .35 if s % 2 else -.35)
    t += BEAT

# 結尾：大和弦 + 衝擊
put(END, kick(1.2), .8)
put(END, noise(2, 1.8, .15), .25)
put(END, tone(hz(36), 4, .01, .9, 'sine'), .4)
for m in (48, 60, 64, 67, 72, 76):
    put(END, tone(hz(m), 5, .02, .7, 'tri'), .08)

# 母帶：軟限幅＋淡出
peak = max(max(abs(x) for x in L), max(abs(x) for x in R)) or 1
g = 1.6 / peak
frames = bytearray()
for i in range(N):
    fade = min(1, (N - i) / (SR * 2.5), i / (SR * .3))
    l = math.tanh(L[i] * g) * .85 * fade
    r = math.tanh(R[i] * g) * .85 * fade
    frames += struct.pack('<hh', int(l * 32767), int(r * 32767))
with wave.open(OUT, 'wb') as w:
    w.setnchannels(2); w.setsampwidth(2); w.setframerate(SR)
    w.writeframes(bytes(frames))
print('music:', OUT)
