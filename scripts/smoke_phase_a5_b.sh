#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

if [ -f .env ]; then
  set -a
  source .env
  set +a
fi

HTTP_PORT="${TOOLHUB_HTTP_PORT:-8080}"
MCP_PORT="${TOOLHUB_MCP_PORT:-8090}"
BASE_URL="http://127.0.0.1:${HTTP_PORT}"

AUTO_START="${SMOKE_AUTO_START:-1}"
if [ "$AUTO_START" = "1" ]; then
  docker compose up -d --build >/dev/null
fi

echo "[smoke] checking /healthz"
health_resp="$(curl -fsS "${BASE_URL}/healthz")"
python3 - <<'PY' "$health_resp"
import json,sys
obj=json.loads(sys.argv[1])
assert obj.get("status") == "ok", obj
PY

echo "[smoke] checking /version"
version_resp="$(curl -fsS "${BASE_URL}/version")"
python3 - <<'PY' "$version_resp"
import json,sys
obj=json.loads(sys.argv[1])
for k in ("version","git_commit","build_time"):
    assert k in obj, obj
PY

repo_value="${REPO_ALLOWLIST%%,*}"
if [ -z "$repo_value" ]; then
  echo "REPO_ALLOWLIST must contain at least one repo in .env" >&2
  exit 1
fi

allowlist_tools=",${TOOL_ALLOWLIST:-},"
pr_tool_enabled=0
if [[ "$allowlist_tools" == *",github.pr.comment.create,"* ]]; then
  pr_tool_enabled=1
fi

echo "[smoke] creating run for repo=${repo_value}"
run_resp="$(curl -fsS -X POST "${BASE_URL}/api/v1/runs" \
  -H 'Content-Type: application/json' \
  -d "{\"repo\":\"${repo_value}\",\"purpose\":\"smoke_phase_a5_b\"}")"
run_id="$(python3 - <<'PY' "$run_resp"
import json,sys
obj=json.loads(sys.argv[1])
rid=obj.get("run_id")
assert rid, obj
print(rid)
PY
)"

echo "[smoke] HTTP dry_run single issue"
issue_resp="$(curl -fsS -X POST "${BASE_URL}/api/v1/runs/${run_id}/issues" \
  -H 'Content-Type: application/json' \
  -d '{"title":"smoke dry run issue","body":"dry run validation","labels":["agent"],"dry_run":true}')"
python3 - <<'PY' "$issue_resp"
import json,sys
obj=json.loads(sys.argv[1])
assert obj.get("ok") is True, obj
meta=obj.get("meta") or {}
assert meta.get("run_id"), obj
assert meta.get("tool_call_id"), obj
assert meta.get("evidence_hash"), obj
assert meta.get("dry_run") is True, obj
result=obj.get("result") or {}
assert "would_create" in result, obj
PY

echo "[smoke] HTTP dry_run batch issues"
batch_resp="$(curl -fsS -X POST "${BASE_URL}/api/v1/runs/${run_id}/issues/batch" \
  -H 'Content-Type: application/json' \
  -d '{"dry_run":true,"issues":[{"title":"smoke b1","body":"dry run"},{"title":"smoke b2","body":"dry run"}]}')"
python3 - <<'PY' "$batch_resp"
import json,sys
obj=json.loads(sys.argv[1])
assert "ok" in obj, obj
meta=obj.get("meta") or {}
assert meta.get("run_id"), obj
assert meta.get("dry_run") is True, obj
result=obj.get("result") or {}
assert result.get("status") in ("ok","partial","fail"), obj
assert isinstance(result.get("results"), list), obj
PY

if [ "$pr_tool_enabled" = "1" ]; then
  echo "[smoke] HTTP dry_run PR summary comment"
  pr_resp="$(curl -fsS -X POST "${BASE_URL}/api/v1/runs/${run_id}/prs/1/comment" \
    -H 'Content-Type: application/json' \
    -d '{"body":"smoke dry run PR summary","dry_run":true}')"
  python3 - <<'PY' "$pr_resp"
import json,sys
obj=json.loads(sys.argv[1])
assert obj.get("ok") is True, obj
meta=obj.get("meta") or {}
assert meta.get("run_id"), obj
assert meta.get("tool_call_id"), obj
assert meta.get("evidence_hash"), obj
assert meta.get("dry_run") is True, obj
result=obj.get("result") or {}
assert "would_comment" in result, obj
PY
else
  echo "[smoke] skipping PR comment checks; github.pr.comment.create not in TOOL_ALLOWLIST"
fi

echo "[smoke] MCP dry_run tool calls"
python3 - <<'PY' "127.0.0.1" "$MCP_PORT" "$repo_value" "${TOOL_ALLOWLIST:-}"
import json, socket, sys

host = sys.argv[1]
port = int(sys.argv[2])
repo = sys.argv[3]
tool_allowlist = sys.argv[4]

def rpc(sock, msg):
    sock.sendall((json.dumps(msg) + "\n").encode())
    data = b""
    while not data.endswith(b"\n"):
        chunk = sock.recv(65536)
        if not chunk:
            break
        data += chunk
    return json.loads(data.decode().strip())

with socket.create_connection((host, port), timeout=10) as s:
    r = rpc(s, {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {}})
    assert "result" in r and "serverInfo" in r["result"], r

    r = rpc(s, {"jsonrpc": "2.0", "id": 2, "method": "tools/list", "params": {}})
    tools = {t["name"] for t in r.get("result", {}).get("tools", [])}
    required = ["runs_create", "github_issues_create", "github_issues_batch_create"]
    for need in required:
        assert need in tools, (need, tools)

    pr_enabled = "github.pr.comment.create" in tool_allowlist.split(",")
    if pr_enabled:
        assert "github_pr_comment_create" in tools, tools

    r = rpc(s, {
        "jsonrpc": "2.0", "id": 3, "method": "tools/call",
        "params": {"name": "runs_create", "arguments": {"repo": repo, "purpose": "smoke_mcp"}},
    })
    run = r.get("result")
    assert isinstance(run, dict) and run.get("run_id"), r
    run_id = run["run_id"]

    r = rpc(s, {
        "jsonrpc": "2.0", "id": 4, "method": "tools/call",
        "params": {
            "name": "github_issues_create",
            "arguments": {"run_id": run_id, "title": "mcp smoke", "body": "dry", "dry_run": True},
        },
    })
    out = r.get("result")
    assert out.get("ok") is True, r
    assert out.get("meta", {}).get("dry_run") is True, r

    r = rpc(s, {
        "jsonrpc": "2.0", "id": 5, "method": "tools/call",
        "params": {
            "name": "github_issues_batch_create",
            "arguments": {
                "run_id": run_id,
                "dry_run": True,
                "issues": [{"title": "m1", "body": "x"}, {"title": "m2", "body": "y"}],
            },
        },
    })
    out = r.get("result")
    assert "result" in out and out.get("meta", {}).get("dry_run") is True, r

    if pr_enabled:
        r = rpc(s, {
            "jsonrpc": "2.0", "id": 6, "method": "tools/call",
            "params": {
                "name": "github_pr_comment_create",
                "arguments": {"run_id": run_id, "pr_number": 1, "body": "mcp dry comment", "dry_run": True},
            },
        })
        out = r.get("result")
        assert out.get("ok") is True and out.get("meta", {}).get("dry_run") is True, r

print("MCP smoke ok")
PY

echo "[smoke] PASS"
