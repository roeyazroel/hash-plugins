#!/usr/bin/env python3
"""Deterministic protocol-v1 all-hooks example; diagnostics must use stderr."""
import json, sys

next_host_id = 1000

def reply(request_id, result):
    print(json.dumps({"jsonrpc": "2.0", "id": request_id, "result": result}), flush=True)

def host_call(parent_request_id, method, params):
    """Minimal synchronous example; production SDKs should multiplex calls."""
    global next_host_id
    next_host_id += 1
    call_id = next_host_id
    params = dict(params)
    params["parent_request_id"] = parent_request_id
    print(json.dumps({"jsonrpc": "2.0", "id": call_id, "method": method, "params": params}), flush=True)
    for raw_response in sys.stdin:
        response = json.loads(raw_response)
        if response.get("id") == call_id and "method" not in response:
            return response.get("result", {})
    return {}

for raw in sys.stdin:
    request = json.loads(raw)
    method = request["method"]
    if method == "$/cancelRequest":
        continue
    if method == "initialize": result = {"protocol_version": 1}
    elif method == "prompt.render": result = {"segments": [{"text": "python", "style": "muted", "placement": "prefix"}]}
    elif method == "editor.suggest": result = {"text": "git status" if request["params"].get("line") == "git" else ""}
    elif method == "completion.provide": result = {"items": [{"label": "status", "insert_text": "status", "replace_start": 4, "replace_end": 6}]}
    elif method == "command.before": result = {"line": "git status", "message": "example transformation"}
    elif method == "command.finished":
        host_call(request["id"], "host.history.query", {"prefix": "git", "cwd": "/work", "limit": 5})
        host_call(request["id"], "host.completion.query", {"line": "git sttaus", "cursor": 4})
        result = {"corrections": ["git status"]}
    elif method == "command.execute":
        host_call(request["id"], "host.environment.get", {"names": ["PATH"]})
        host_call(request["id"], "host.output.write", {"stream": "stdout", "text": "all-hooks example\n"})
        result = {"exit_code": 0}
    else: result = {}
    if "id" in request: reply(request["id"], result)
