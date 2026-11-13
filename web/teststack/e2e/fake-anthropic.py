"""A stand-in Anthropic Messages API for the test stack: every POST to
/v1/messages streams one short text reply. Requests are logged (model and the
last user text) to $FAKE_ANTHROPIC_LOG, by default in the temp folder.
Nothing leaves the machine."""
import json, os, sys, time
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOG = os.environ.get("FAKE_ANTHROPIC_LOG") or os.path.join(os.environ.get("TMPDIR", "/tmp"), "fake-anthropic.log")

def last_user_text(body):
    for message in reversed(body.get("messages", [])):
        if message.get("role") != "user":
            continue
        content = message.get("content")
        if isinstance(content, str):
            return content
        for block in reversed(content or []):
            if block.get("type") == "text":
                return block.get("text", "")
    return ""

class Handler(BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass

    def do_POST(self):
        length = int(self.headers.get("content-length", 0))
        body = json.loads(self.rfile.read(length) or b"{}")
        asked = last_user_text(body)
        with open(LOG, "a") as log:
            log.write(json.dumps({"t": time.strftime("%H:%M:%S"), "path": self.path, "model": body.get("model"), "tools": len(body.get("tools", [])), "asked": asked[-160:]}) + "\n")
        reply = os.environ.get("FAKE_REPLY", "Fake Agentman here. Two agents are idle and nothing needs you.")
        events = [
            ("message_start", {"type": "message_start", "message": {"id": "msg_fake", "type": "message", "role": "assistant", "model": body.get("model", "fake"), "content": [], "stop_reason": None, "stop_sequence": None, "usage": {"input_tokens": 10, "output_tokens": 1}}}),
            ("content_block_start", {"type": "content_block_start", "index": 0, "content_block": {"type": "text", "text": ""}}),
        ]
        for i in range(0, len(reply), 12):
            events.append(("content_block_delta", {"type": "content_block_delta", "index": 0, "delta": {"type": "text_delta", "text": reply[i:i + 12]}}))
        events += [
            ("content_block_stop", {"type": "content_block_stop", "index": 0}),
            ("message_delta", {"type": "message_delta", "delta": {"stop_reason": "end_turn", "stop_sequence": None}, "usage": {"output_tokens": 20}}),
            ("message_stop", {"type": "message_stop"}),
        ]
        self.send_response(200)
        self.send_header("content-type", "text/event-stream")
        self.send_header("cache-control", "no-cache")
        self.end_headers()
        for name, data in events:
            self.wfile.write(f"event: {name}\ndata: {json.dumps(data)}\n\n".encode())
            self.wfile.flush()
            time.sleep(0.03)

ThreadingHTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 18998), Handler).serve_forever()
