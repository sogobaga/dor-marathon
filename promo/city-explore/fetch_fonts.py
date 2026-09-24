"""下載 Noto Sans TC / Space Grotesk 的 Google Fonts 切片到 fonts/，並產生改寫成本機路徑的 local.css。"""
import hashlib, os, re, urllib.request
from concurrent.futures import ThreadPoolExecutor

HERE = os.path.dirname(os.path.abspath(__file__))
OUT = os.path.join(HERE, 'fonts')
CSS = 'https://fonts.googleapis.com/css2?family=Noto+Sans+TC:wght@500;700;900&family=Space+Grotesk:wght@700&display=block'
UA = 'Mozilla/5.0 (X11; Linux x86_64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/130 Safari/537.36'

def get(url):
    return urllib.request.urlopen(urllib.request.Request(url, headers={'User-Agent': UA})).read()

os.makedirs(OUT, exist_ok=True)
css = get(CSS).decode()
urls = sorted(set(re.findall(r'https://fonts\.gstatic\.com[^)]*', css)))
name = lambda u: hashlib.md5(u.encode()).hexdigest()[:12] + '.woff2'

def dl(u):
    with open(os.path.join(OUT, name(u)), 'wb') as f:
        f.write(get(u))

with ThreadPoolExecutor(16) as ex:
    list(ex.map(dl, urls))
css = re.sub(r'https://fonts\.gstatic\.com[^)]*', lambda m: name(m.group(0)), css)
open(os.path.join(OUT, 'local.css'), 'w').write(css)
print(f'fonts: {len(urls)} files')
