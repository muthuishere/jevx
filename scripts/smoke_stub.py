#!/usr/bin/env python3
"""A stand-in System One endpoint for the install smoke test: answers every question it is asked with a fixed,
recognisable value (noul 0.9, the first choice at 0.9, the top score level). It checks the request shape and refuses
a malformed one, so a client that sends the wrong thing fails the smoke. It is a contract stub, not a model."""
import json, sys
from http.server import BaseHTTPRequestHandler, HTTPServer

class H(BaseHTTPRequestHandler):
    def do_POST(self):
        try:
            req = json.loads(self.rfile.read(int(self.headers.get("Content-Length", 0))))
            assert req.get("state") and isinstance(req.get("questions"), dict) and req["questions"]
            ans = {}
            for k, q in req["questions"].items():
                t = q["type"]
                if t == "noul":
                    ans[k] = {"type": t, "noul": 0.9, "confidence": 0.9}
                elif t == "choice":
                    ans[k] = {"type": t, "choice": next(iter(q["criteria"])), "confidence": 0.9}
                else:
                    ans[k] = {"type": t, "score": float(len(q["criteria"]) - 1), "confidence": 0.9}
            body, code = json.dumps({"model": "stub", "answers": ans}).encode(), 200
        except Exception as e:  # a malformed request is the client's bug: say so
            body, code = json.dumps({"error": str(e)}).encode(), 400
        self.send_response(code)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(body)

    def log_message(self, *a):
        pass

HTTPServer(("127.0.0.1", int(sys.argv[1]) if len(sys.argv) > 1 else 21999), H).serve_forever()
