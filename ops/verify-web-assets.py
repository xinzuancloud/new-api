#!/usr/bin/env python3
"""Read-only deployment check for empty SPA pages and missing entry assets."""
import argparse
import json
from html.parser import HTMLParser
from urllib.parse import urljoin, urlsplit
from urllib.request import Request, urlopen


class EntryAssets(HTMLParser):
    def __init__(self):
        super().__init__()
        self.root = False
        self.assets = []

    def handle_starttag(self, tag, attrs):
        attrs = dict(attrs)
        self.root |= attrs.get("id") == "root"
        if tag == "script" and attrs.get("src"):
            self.assets.append((attrs["src"], "script"))
        if tag == "link" and attrs.get("rel") == "stylesheet" and attrs.get("href"):
            self.assets.append((attrs["href"], "style"))


def verify(base):
    with urlopen(base, timeout=15) as response:
        html = response.read(1024 * 1024)
        if response.status != 200 or "text/html" not in response.headers.get("Content-Type", ""):
            raise ValueError("homepage is not HTTP200 HTML")
    entry = EntryAssets()
    entry.feed(html.decode("utf-8"))
    if not html.strip() or not entry.root or not any(kind == "script" for _, kind in entry.assets):
        raise ValueError("homepage is empty or missing the SPA root/entry script")
    assets = []
    for reference, kind in dict.fromkeys(entry.assets):
        url = urljoin(base, reference)
        if urlsplit(url).netloc != urlsplit(base).netloc:
            continue
        with urlopen(Request(url, headers={"Range": "bytes=0-2047"}), timeout=15) as response:
            sample = response.read(2048)
            mime = response.headers.get_content_type()
            expected = {"text/css"} if kind == "style" else {"text/javascript", "application/javascript", "application/x-javascript"}
            if response.status not in (200, 206) or not sample.strip() or mime not in expected:
                raise ValueError("entry asset is empty, missing, or has the wrong content type")
            assets.append({"path": urlsplit(url).path, "status": response.status, "mime": mime})
    if not assets:
        raise ValueError("no same-origin entry assets were verified")
    return {"healthy": True, "html_bytes": len(html), "assets": assets}


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("url")
    args = parser.parse_args()
    try:
        print(json.dumps(verify(args.url), ensure_ascii=False))
    except Exception as error:
        print(json.dumps({"healthy": False, "error": str(error)}, ensure_ascii=False))
        raise SystemExit(1)
