#!/usr/bin/env python3
"""Preview site/ with the same clean page URLs as the deployed site."""
import argparse
from functools import partial
from http.server import SimpleHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path
from urllib.parse import urlsplit

ROOT = Path(__file__).resolve().parents[1] / 'site'
ROUTES = {'/': '/index.html', '/mac': '/mac.html', '/ios': '/ios.html',
          '/start': '/start.html', '/privacy': '/privacy.html',
          '/install': '/install.sh', '/iphone': '/ios.html'}

class PreviewHandler(SimpleHTTPRequestHandler):
    def do_GET(self):
        self.path = ROUTES.get(urlsplit(self.path).path, self.path)
        super().do_GET()

    def do_HEAD(self):
        self.path = ROUTES.get(urlsplit(self.path).path, self.path)
        super().do_HEAD()

    def end_headers(self):
        self.send_header('Cache-Control', 'no-store')
        super().end_headers()

class PreviewServer(ThreadingHTTPServer):
    # The default backlog of five drops connections when a browser asks for
    # a page's images at once: logos showed broken in the preview only.
    request_queue_size = 128
    daemon_threads = True

if __name__ == '__main__':
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--port', type=int, default=8766)
    args = parser.parse_args()
    server = PreviewServer(('127.0.0.1', args.port), partial(PreviewHandler, directory=str(ROOT)))
    print(f'Agentman preview: http://localhost:{args.port}', flush=True)
    try:
        server.serve_forever()
    except KeyboardInterrupt:
        pass
    finally:
        server.server_close()
