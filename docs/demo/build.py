"""Build the interactive demo into one embeddable HTML file.

    python3 build.py

Inputs: captures/ (real terminal output from a local inz instance), shots/ (real dashboard
screenshots), player.src.html. Output: ../site/public/demo/instancez-demo.html (single file: fonts, images and
data are inlined, so it works offline and inside an iframe).
"""
import base64, html, json, pathlib, re

here = pathlib.Path(__file__).parent
cap = here / 'captures'
fonts_dir = here / 'assets' / 'fonts'
logo = here.parent / 'site' / 'src' / 'assets' / 'logo.svg'

ANSI = re.compile(r'\x1b\[([0-9;]*)m')

def ansi_to_html(line):
    out, i, open_ = [], 0, False
    for m in ANSI.finditer(line):
        out.append(html.escape(line[i:m.start()]))
        i = m.end()
        codes = [c for c in m.group(1).split(';') if c != ''] or ['0']
        if open_:
            out.append('</span>'); open_ = False
        cls = []
        for c in codes:
            c = int(c)
            if c == 1: cls.append('b')
            elif c == 2: cls.append('d')
            elif 30 <= c <= 37 or 90 <= c <= 97: cls.append('f%d' % c)
        if cls:
            out.append('<span class="%s">' % ' '.join(cls)); open_ = True
    out.append(html.escape(line[i:]))
    if open_: out.append('</span>')
    return ''.join(out)

def lines_of(name):
    raw = (cap / name).read_text(errors='replace').replace('\r', '')
    ls = [l for l in raw.split('\n') if not l.startswith('Script started') and not l.startswith('Script done')]
    while ls and ls[-1] == '': ls.pop()
    return ls

def pack(ls):
    res = []
    for l in ls:
        l = re.sub(r'(inz_secret_)[A-Za-z0-9_-]{6}[A-Za-z0-9_-]+', r'\1••••••••••••', l)
        res.append({'h': ansi_to_html(l), 't': ANSI.sub('', l)})
    return res

T = {}
for k, f in [('version', 'version.raw'), ('init', 'init.raw'), ('validate_ok', 'validate_ok.raw'), ('validate_bad', 'validate_bad.raw'),
             ('vet', 'vet.raw'), ('serve', 'serve.raw'), ('curl_prod', 'curl_prod.raw'), ('cloud_whoami', 'cloud_whoami.raw'), ('js_run', 'js_run.raw')]:
    T[k] = pack(lines_of(f))

plan = lines_of('validate_plan.raw')
ix = next(i for i, l in enumerate(plan) if 'ALTER TABLE todos ADD COLUMN due_date' in l)
serve = lines_of('serve.raw')
T['serve'] = pack(serve[:next(i for i, l in enumerate(serve) if 'HTTP server listening' in l) + 1])
T['validate_plan'] = pack(plan[:2] + [''] + ['\x1b[2m…  (schema bootstrap statements trimmed)\x1b[0m', ''] + [plan[ix]] + ['', '\x1b[2m…\x1b[0m'])

dev = lines_of('dev.raw')
stop = next(i for i, l in enumerate(dev) if 'Watching for changes' in l)
T['dev'] = pack(dev[:stop + 1])
T['reload'] = pack([l for l in dev if 'config reloaded successfully' in l or 'functions: reloaded' in l])

def b64(p, mime):
    return 'data:%s;base64,%s' % (mime, base64.b64encode(p.read_bytes()).decode())

shots = {p.stem: b64(p, 'image/webp') for p in sorted((here / 'shots').glob('*.webp'))}
files = {'yaml0': (cap / 'yaml0.yaml').read_text(), 'yaml1': (cap / 'yaml1.yaml').read_text(), 'app': (cap / 'app.mjs').read_text()}
data = {'term': T, 'shots': shots, 'files': files, 'logo': b64(logo, 'image/svg+xml')}

fonts = ''
for fam, w, f in [('Brico', 400, 'BricolageGrotesque-400.ttf'), ('Brico', 600, 'BricolageGrotesque-600.ttf'), ('Brico', 800, 'BricolageGrotesque-800.ttf'), ('JBM', 400, 'JetBrainsMono-400.ttf'), ('JBM', 500, 'JetBrainsMono-500.ttf')]:
    fonts += "@font-face{font-family:%s;font-weight:%d;font-display:swap;src:url(%s) format('truetype')}\n" % (fam, w, b64(fonts_dir / f, 'font/ttf'))

src = (here / 'player.src.html').read_text()
src = src.replace('/*__FONTS__*/', fonts).replace('"__DATA__"', json.dumps(data, ensure_ascii=False).replace('</', '<\\/'))
out = here.parent / 'site' / 'public' / 'demo' / 'instancez-demo.html'
out.parent.mkdir(parents=True, exist_ok=True)
out.write_text(src)
print(out, len(src) // 1024, 'KB')
