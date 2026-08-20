#!/usr/bin/env python3
"""Builds site/blog/ from the sources in site/blog/posts/.

    python3 site/build-blog.py

One generator rather than nine hand-written pages. The rest of the site is
hand-written HTML on purpose — each page is a different composition and the
comments explain why it is shaped the way it is — but nine articles are the
same composition nine times, and a stylesheet copied nine times drifts nine
ways.

Each post is a markdown file with a YAML-ish header. The generator emits the
page, its Article and FAQPage structured data, and the index.

Every page is self-contained: no external stylesheet, no script, no font
request. That is the same constraint the rest of the site works under, and it
is also why these pages load in one round trip.
"""
import html
import json
import os
import re
import subprocess
import sys

ROOT = os.path.dirname(os.path.abspath(__file__))
POSTS = os.path.join(ROOT, "blog", "posts")
SITE = "https://getgoguma.com"
DATE = subprocess.run(["date", "+%Y-%m-%d"], capture_output=True, text=True).stdout.strip()

# ── the sheet ─────────────────────────────────────────────────────────────
# The document pages' sheet (privacy, terms), plus what prose needs: tables,
# code blocks, pull quotes. No cards and no borders, per DESIGN.md — sections
# are separated by whitespace and hairline rules and nothing else.
CSS = """
:root{
  --ground:#EFE4F0; --ink:#241A2C; --ink-2:#6B6072;
  --card:#FFFFFF; --rule:#E3D1E6;
  --accent:#93507F; --ember:#B4531A;
  --max:720px;
  --sans:-apple-system,BlinkMacSystemFont,"SF Pro Display","Helvetica Neue",Helvetica,Arial,sans-serif;
  --mono:ui-monospace,SFMono-Regular,"SF Mono",Menlo,monospace;
}
*{box-sizing:border-box}
html{-webkit-text-size-adjust:100%}
body{margin:0;background:var(--ground);color:var(--ink);font-family:var(--sans);
  -webkit-font-smoothing:antialiased;min-height:100vh;display:flex;flex-direction:column}
img{max-width:100%;display:block}
.wrap{width:100%;max-width:var(--max);margin:0 auto;padding-left:24px;padding-right:24px}
.page{flex:1}
h1,h2,h3{margin:0;letter-spacing:-.035em;line-height:1.06;font-weight:600}
h1{font-size:clamp(32px,4.6vw,46px)}
h2{font-size:clamp(21px,2.3vw,26px);letter-spacing:-.03em;margin:clamp(40px,5vh,54px) 0 0}
h3{font-size:clamp(17px,1.8vw,19px);letter-spacing:-.022em;margin:30px 0 0}
p{margin:15px 0 0;font-size:16.5px;line-height:1.65;color:var(--ink-2)}
p strong,li strong{color:var(--ink);font-weight:600}
ul,ol{margin:15px 0 0;padding-left:22px}
li{font-size:16.5px;line-height:1.65;color:var(--ink-2);margin:8px 0 0}
a{color:var(--accent)}
a:hover{color:#653A69}
code{overflow-wrap:anywhere;font-family:var(--mono);font-size:.9em;
  background:color-mix(in srgb,var(--accent) 10%,transparent);
  padding:1px 6px;border-radius:5px;color:var(--ink)}
pre{margin:18px 0 0;padding:18px 20px;overflow-x:auto;border-radius:12px;
  background:#211630;color:#EFE4F0;font-family:var(--mono);font-size:14px;line-height:1.6}
pre code{background:none;padding:0;color:inherit;font-size:inherit}
/* Tables: hairline rules only, and they scroll rather than push the page wide. */
.tw{overflow-x:auto;margin:20px 0 0}
table{border-collapse:collapse;width:100%;font-size:15px}
th,td{text-align:left;padding:10px 14px 10px 0;border-bottom:1px solid var(--rule);
  color:var(--ink-2);line-height:1.5;vertical-align:top}
th{color:var(--ink);font-weight:600;font-size:13.5px;letter-spacing:.01em}
td strong{color:var(--ink)}
blockquote{margin:20px 0 0;padding:0 0 0 20px;border-left:1px solid var(--rule)}
blockquote p{margin:0;color:var(--ink);font-size:17px}
blockquote p + p{margin-top:10px}
.top{max-width:none;padding:14px 28px 0}
.home{display:inline-flex;align-items:center;gap:9px;font-size:23px;font-weight:600;
  letter-spacing:-.022em;color:var(--ink);text-decoration:none;transition:opacity .18s}
.home img{width:23px;height:23px}
.home:hover{opacity:.7}
.head{padding-top:clamp(30px,6vh,64px)}
.crumbs{margin:0 0 14px;font-size:13.5px;color:rgba(36,26,44,.66)}
.crumbs a{color:rgba(36,26,44,.66);text-decoration:none}
.crumbs a:hover{color:var(--accent);text-decoration:underline}
.crumbs span{margin:0 6px}
.stamp{margin:16px 0 0;font-family:var(--mono);font-size:12.5px;
  color:rgba(36,26,44,.66);letter-spacing:.01em}
/* The answer, before the article. Someone who came from a search result wants
   it in the first screen, not after a preamble. */
.answer{margin:clamp(24px,4vh,34px) 0 0;padding-bottom:clamp(24px,4vh,34px);
  border-bottom:1px solid var(--rule)}
.answer p{margin:0;color:var(--ink);font-size:clamp(17px,1.9vw,19.5px);
  line-height:1.52;letter-spacing:-.012em}
.answer p + p{margin-top:11px}
.body{padding-bottom:clamp(30px,6vh,60px)}
/* The one place goguma is pitched, at the foot, after the answer is given. */
.cta{margin:clamp(44px,6vh,64px) 0 0;padding-top:clamp(26px,4vh,36px);
  border-top:1px solid var(--rule)}
.cta p{margin:0;font-size:16px}
.cta .b{display:inline-flex;align-items:center;gap:8px;margin-top:16px;height:46px;
  padding:0 20px;border-radius:12px;background:#211630;color:#fff;font-size:15px;
  font-weight:500;text-decoration:none;transition:background .18s}
.cta .b:hover{background:#160E22;color:#fff}
.more{margin:clamp(34px,5vh,48px) 0 0;padding-top:26px;border-top:1px solid var(--rule)}
.more h2{margin:0 0 4px;font-size:15px;letter-spacing:.01em}
.more a{display:block;margin-top:9px;font-size:15.5px;text-decoration:none}
.more a:hover{text-decoration:underline}
.idx a{display:block;margin-top:26px;text-decoration:none;color:var(--ink)}
.idx a:hover .t{color:var(--accent)}
.idx .t{font-size:20px;font-weight:600;letter-spacing:-.028em;line-height:1.2}
.idx .d{margin-top:6px;font-size:15.5px;line-height:1.55;color:var(--ink-2)}
footer{padding:clamp(20px,4vh,52px) 0 22px;text-align:center;font-size:13px;
  color:rgba(36,26,44,.66)}
footer a{color:rgba(36,26,44,.64)}
.foot-links{margin:0;display:flex;gap:10px;justify-content:center;flex-wrap:wrap}
.foot-links a{text-decoration:none;border-bottom:1px solid rgba(36,26,44,.18);padding-bottom:1px}
.foot-fine{margin:9px 0 0;font-size:12px;color:rgba(36,26,44,.66)}
"""

FOOT = """<footer>
  <p class="foot-links">
    <a href="{up}">goguma</a> ·
    <a href="{blog}">Writing</a> ·
    <a href="https://github.com/junnam586/goguma">Source</a> ·
    <a href="https://github.com/junnam586/goguma/blob/main/SECURITY.md">Security</a> ·
    <a href="{up}privacy/">Privacy</a> ·
    <a href="{up}terms/">Terms</a>
  </p>
  <p class="foot-fine">goguma is free and open source, under the MIT licence.</p>
</footer>"""


def parse(src):
    """Split a post into its header and its body."""
    m = re.match(r"^---\n(.*?)\n---\n(.*)$", src, re.S)
    if not m:
        raise SystemExit("post is missing its --- header ---")
    head, body = m.group(1), m.group(2)
    meta, key = {}, None
    faq, q = [], None
    for line in head.split("\n"):
        if line.startswith("faq:"):
            key = "faq"
            continue
        if key == "faq":
            if re.match(r"\s*-\s*q:", line):
                if q:
                    faq.append(q)
                q = {"q": line.split("q:", 1)[1].strip()}
            elif re.match(r"\s+a:", line) and q is not None:
                q["a"] = line.split("a:", 1)[1].strip()
            continue
        if ":" in line:
            k, v = line.split(":", 1)
            meta[k.strip()] = v.strip()
    if q:
        faq.append(q)
    meta["faq"] = faq
    return meta, body


def inline(t):
    """Inline markdown. Escaped first, so a post can never inject markup."""
    t = html.escape(t, quote=False)
    t = re.sub(r"`([^`]+)`", r"<code>\1</code>", t)
    t = re.sub(r"\*\*([^*]+)\*\*", r"<strong>\1</strong>", t)
    t = re.sub(r"\[([^\]]+)\]\(([^)]+)\)", r'<a href="\2">\1</a>', t)
    return t


def render(body):
    """A markdown subset: headings, lists, tables, code fences, quotes."""
    out, lines, i = [], body.split("\n"), 0
    while i < len(lines):
        ln = lines[i]
        if ln.startswith("```"):
            buf = []
            i += 1
            while i < len(lines) and not lines[i].startswith("```"):
                buf.append(html.escape(lines[i]))
                i += 1
            out.append("<pre><code>" + "\n".join(buf) + "</code></pre>")
        elif ln.startswith("|"):
            rows = []
            while i < len(lines) and lines[i].startswith("|"):
                rows.append(lines[i])
                i += 1
            i -= 1
            cells = [[c.strip() for c in r.strip("|").split("|")] for r in rows]
            head = cells[0]
            rest = [r for r in cells[2:]] if len(cells) > 2 else []
            t = ["<div class=\"tw\"><table><thead><tr>"]
            t += [f"<th>{inline(c)}</th>" for c in head]
            t.append("</tr></thead><tbody>")
            for r in rest:
                t.append("<tr>" + "".join(f"<td>{inline(c)}</td>" for c in r) + "</tr>")
            t.append("</tbody></table></div>")
            out.append("".join(t))
        elif ln.startswith("> "):
            buf = []
            while i < len(lines) and lines[i].startswith("> "):
                buf.append(inline(lines[i][2:]))
                i += 1
            i -= 1
            out.append("<blockquote>" + "".join(f"<p>{b}</p>" for b in buf if b) + "</blockquote>")
        elif re.match(r"^(-|\d+\.) ", ln):
            ordered = bool(re.match(r"^\d+\. ", ln))
            buf = []
            while i < len(lines) and re.match(r"^(-|\d+\.) ", lines[i]):
                buf.append(inline(re.sub(r"^(-|\d+\.) ", "", lines[i])))
                i += 1
            i -= 1
            tag = "ol" if ordered else "ul"
            out.append(f"<{tag}>" + "".join(f"<li>{b}</li>" for b in buf) + f"</{tag}>")
        elif ln.startswith("### "):
            out.append(f"<h3>{inline(ln[4:])}</h3>")
        elif ln.startswith("## "):
            out.append(f"<h2>{inline(ln[3:])}</h2>")
        elif ln.strip():
            buf = []
            while i < len(lines) and lines[i].strip() and not re.match(
                    r"^(#|-|\d+\.|\||>|```)", lines[i]):
                buf.append(lines[i].strip())
                i += 1
            i -= 1
            out.append(f"<p>{inline(' '.join(buf))}</p>")
        i += 1
    return "\n".join(out)


def page(meta, body_html, posts):
    slug, title = meta["slug"], meta["title"]
    url = f"{SITE}/blog/{slug}/"
    ld = [{
        "@context": "https://schema.org", "@type": "TechArticle",
        "headline": title, "description": meta["description"],
        "datePublished": meta["date"], "dateModified": meta["date"],
        "author": {"@type": "Person", "name": "Jun Nam"},
        "publisher": {"@type": "Organization", "name": "goguma",
                      "url": SITE + "/"},
        "mainEntityOfPage": {"@type": "WebPage", "@id": url},
        "image": f"{SITE}/assets/og.png",
        "about": {"@type": "SoftwareApplication", "name": "goguma",
                  "operatingSystem": "macOS 14.0 or later",
                  "applicationCategory": "UtilitiesApplication"},
    }]
    # Breadcrumbs, unlike FAQ, still produce a rich result: Google replaces the
    # raw URL under the title with the trail. FAQ rich results were withdrawn
    # entirely in May 2026, so FAQPage below is carried for Bing and the RAG
    # crawlers rather than for anything Google will draw.
    ld.append({
        "@context": "https://schema.org", "@type": "BreadcrumbList",
        "itemListElement": [
            {"@type": "ListItem", "position": 1, "name": "goguma", "item": SITE + "/"},
            {"@type": "ListItem", "position": 2, "name": "Writing", "item": SITE + "/blog/"},
            {"@type": "ListItem", "position": 3, "name": title, "item": url},
        ],
    })
    if meta["faq"]:
        ld.append({
            "@context": "https://schema.org", "@type": "FAQPage",
            "mainEntity": [{"@type": "Question", "name": f["q"],
                            "acceptedAnswer": {"@type": "Answer", "text": f["a"]}}
                           for f in meta["faq"]],
        })
    scripts = "\n".join(
        '<script type="application/ld+json">\n' + json.dumps(x, indent=1) + "\n</script>"
        for x in ld)

    faq_html = ""
    if meta["faq"]:
        faq_html = "<h2>Common questions</h2>" + "".join(
            f"<h3>{inline(f['q'])}</h3><p>{inline(f['a'])}</p>" for f in meta["faq"])

    related = [p for p in posts if p["slug"] != slug][:3]
    more = ""
    if related:
        more = ('<div class="more"><h2>Related</h2>'
                + "".join(f'<a href="../{p["slug"]}/">{html.escape(p["title"])}</a>'
                          for p in related) + "</div>")

    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{html.escape(title)}</title>
<meta name="description" content="{html.escape(meta['description'])}">
<link rel="icon" href="../../assets/potato.png">
<meta property="og:title" content="{html.escape(title)}">
<meta property="og:description" content="{html.escape(meta['description'])}">
<meta property="og:image" content="{SITE}/assets/og.png">
<meta property="og:image:width" content="1200">
<meta property="og:image:height" content="630">
<meta property="og:url" content="{url}">
<meta property="og:type" content="article">
<meta property="og:site_name" content="goguma">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:title" content="{html.escape(title)}">
<meta name="twitter:image" content="{SITE}/assets/og.png">
<link rel="alternate" type="application/atom+xml" title="goguma · writing" href="../../feed.xml">
<link rel="canonical" href="{url}">
{scripts}
<style>{CSS}</style>
</head>
<body>
<div class="page">
  <div class="wrap top">
    <a class="home" href="../../"><img src="../../assets/potato.webp" alt="" width="253" height="249">goguma</a>
  </div>
  <div class="wrap head">
    <h1>{html.escape(title)}</h1>
    <nav class="crumbs" aria-label="Breadcrumb">
      <a href="../../">goguma</a> <span aria-hidden="true">›</span>
      <a href="../">Writing</a>
    </nav>
    <p class="stamp">{meta['date']}</p>
    <div class="answer"><p>{inline(meta['answer'])}</p></div>
  </div>
  <div class="wrap body">
{body_html}
{faq_html}
    <div class="cta">
      <p><strong>goguma</strong> does this for you: it reads the scheduled jobs
        already on your Mac, wakes the machine shortly before each one is due,
        holds sleep off while it runs, and lets it sleep again afterwards. Free
        and open source, macOS 14+.</p>
      <a class="b" href="../../">Get goguma →</a>
    </div>
{more}
  </div>
</div>
{FOOT.format(up="../../", blog="../")}
</body>
</html>
"""


def index(posts):
    items = "".join(
        f'<a href="{p["slug"]}/"><span class="t">{html.escape(p["title"])}</span>'
        f'<span class="d">{html.escape(p["description"])}</span></a>' for p in posts)
    ld = {"@context": "https://schema.org", "@type": "Blog",
          "name": "goguma · writing", "url": f"{SITE}/blog/",
          "blogPost": [{"@type": "BlogPosting", "headline": p["title"],
                        "url": f'{SITE}/blog/{p["slug"]}/',
                        "datePublished": p["date"]} for p in posts]}
    return f"""<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>goguma · writing</title>
<meta name="description" content="How macOS sleep, cron, launchd and coding agents actually interact — and what to do about it.">
<link rel="icon" href="../assets/potato.png">
<meta property="og:title" content="goguma · writing">
<meta property="og:description" content="How macOS sleep, cron, launchd and coding agents actually interact.">
<meta property="og:image" content="{SITE}/assets/og.png">
<meta property="og:url" content="{SITE}/blog/">
<meta property="og:type" content="website">
<meta property="og:site_name" content="goguma">
<meta name="twitter:card" content="summary_large_image">
<meta name="twitter:image" content="{SITE}/assets/og.png">
<link rel="canonical" href="{SITE}/blog/">
<script type="application/ld+json">
{json.dumps(ld, indent=1)}
</script>
<style>{CSS}</style>
</head>
<body>
<div class="page">
  <div class="wrap top">
    <a class="home" href="../"><img src="../assets/potato.webp" alt="" width="253" height="249">goguma</a>
  </div>
  <div class="wrap head">
    <h1>Writing</h1>
    <div class="answer"><p>What actually happens when a Mac sleeps through the
      work it was supposed to do, why the usual advice does not fix it, and how
      each of the tools in this area really behaves.</p></div>
  </div>
  <div class="wrap body idx">{items}</div>
</div>
{FOOT.format(up="../", blog="./")}
</body>
</html>
"""


def main():
    srcs = sorted(f for f in os.listdir(POSTS) if f.endswith(".md"))
    metas = []
    for f in srcs:
        meta, _ = parse(open(os.path.join(POSTS, f)).read())
        metas.append(meta)
    metas.sort(key=lambda m: int(m.get("order", 99)))
    for f in srcs:
        meta, body = parse(open(os.path.join(POSTS, f)).read())
        d = os.path.join(ROOT, "blog", meta["slug"])
        os.makedirs(d, exist_ok=True)
        open(os.path.join(d, "index.html"), "w").write(page(meta, render(body), metas))
    open(os.path.join(ROOT, "blog", "index.html"), "w").write(index(metas))

    # The sitemap is generated here rather than maintained by hand, because a
    # hand-maintained one drifts the first time a post is added and then lies
    # quietly. The four static pages are listed explicitly; everything under
    # /blog/ comes from the posts that actually exist.
    static = [("", "weekly", "1.0"), ("updates/", "weekly", "0.6"),
              ("privacy/", "yearly", "0.3"), ("terms/", "yearly", "0.3"),
              ("blog/", "weekly", "0.8")]
    urls = [f"""  <url>
    <loc>{SITE}/{p}</loc>
    <lastmod>{DATE}</lastmod>
    <changefreq>{c}</changefreq>
    <priority>{pr}</priority>
  </url>""" for p, c, pr in static]
    urls += [f"""  <url>
    <loc>{SITE}/blog/{m['slug']}/</loc>
    <lastmod>{m['date']}</lastmod>
    <changefreq>monthly</changefreq>
    <priority>0.7</priority>
  </url>""" for m in metas]
    open(os.path.join(ROOT, "sitemap.xml"), "w").write(
        '<?xml version="1.0" encoding="UTF-8"?>\n'
        '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'
        + "\n".join(urls) + "\n</urlset>\n")

    # An Atom feed. Not for readers — for the aggregators and crawlers that
    # still discover new pages this way, and because a feed is the cheapest
    # freshness signal a static site can emit.
    def esc(t):
        return (t.replace("&", "&amp;").replace("<", "&lt;").replace(">", "&gt;"))
    entries = "".join(f"""  <entry>
    <title>{esc(m['title'])}</title>
    <link href="{SITE}/blog/{m['slug']}/"/>
    <id>{SITE}/blog/{m['slug']}/</id>
    <updated>{m['date']}T00:00:00Z</updated>
    <summary>{esc(m['description'])}</summary>
  </entry>
""" for m in metas)
    open(os.path.join(ROOT, "feed.xml"), "w").write(
        f"""<?xml version="1.0" encoding="utf-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <title>goguma · writing</title>
  <subtitle>How macOS sleep, cron, launchd and coding agents actually interact.</subtitle>
  <link href="{SITE}/feed.xml" rel="self"/>
  <link href="{SITE}/blog/"/>
  <id>{SITE}/blog/</id>
  <updated>{DATE}T00:00:00Z</updated>
  <author><name>Jun Nam</name></author>
{entries}</feed>
""")

    print(f"built {len(srcs)} posts + index + sitemap ({len(urls)} urls) + feed")
    for m in metas:
        print(f"  /blog/{m['slug']}/")


if __name__ == "__main__":
    main()
