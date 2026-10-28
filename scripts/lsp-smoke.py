#!/usr/bin/env python3
"""Exercise the shipped stdio server, including navigation and a host profile."""
import json
import subprocess
import sys

profile = {"name": "smoke", "symbols": [{"name": "editor-message", "doc": "Show editor message"}]}
messages = [
    {"id": 1, "method": "initialize", "params": {"initializationOptions": {"hostProfile": profile}}},
    {"method": "initialized"},
    {"method": "textDocument/didOpen", "params": {"textDocument": {"uri": "file:///smoke.scm", "languageId": "scheme", "version": 1, "text": "(define value 42) (editor-message value)"}}},
    {"id": 2, "method": "textDocument/definition", "params": {"textDocument": {"uri": "file:///smoke.scm"}, "position": {"line": 0, "character": 34}}},
    {"id": 3, "method": "textDocument/hover", "params": {"textDocument": {"uri": "file:///smoke.scm"}, "position": {"line": 0, "character": 19}}},
    {"id": 4, "method": "shutdown"},
    {"method": "exit"},
]
frames = []
for message in messages:
    data = json.dumps(message).encode()
    frames.append(f"Content-Length: {len(data)}\r\n\r\n".encode() + data)
result = subprocess.run([sys.argv[1]], input=b"".join(frames), capture_output=True, timeout=15, check=True)
remaining = result.stdout
responses = {}
while remaining:
    header, remaining = remaining.split(b"\r\n\r\n", 1)
    length = int(header.split(b":", 1)[1])
    message = json.loads(remaining[:length])
    remaining = remaining[length:]
    if "id" in message:
        responses[message["id"]] = message
assert len(responses[2]["result"]) == 1, responses
assert responses[2]["result"][0]["range"]["start"]["character"] == 8, responses
assert "Show editor message" in responses[3]["result"]["contents"]["value"], responses
assert responses[4]["result"] is None, responses
print("Brote stdio LSP smoke passed")
