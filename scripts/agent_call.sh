#!/usr/bin/env bash
# agent_call.sh — 以真实 agent 的方式调用 molasearch 的 stdio MCP 服务。
# 与 Claude Code 等客户端完全同构：initialize → tools/list → tools/call ×N。
# 用法: agent_call.sh <exe> <out.jsonl>
set -u
EXE="$1"; OUT="$2"
{
  printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"aihot-agent","version":"1.0.0"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","method":"notifications/initialized"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":2,"method":"tools/list"}'
  printf '%s\n' '{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"magpie_investigate","arguments":{"query":"GitHub 上 star 最多的 Go 图数据库仓库是哪个，最新版本号是多少","modeId":"M23"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"magpie_search_fast","arguments":{"query":"PostgreSQL 17 pgvector HNSW 混合检索 最佳实践"}}}'
  printf '%s\n' '{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"magpie_investigate","arguments":{"query":"现代向量数据库 Milvus 与 Elasticsearch kNN 的混合检索差异","modeId":"M24"}}}'
  sleep 60
} | "$EXE" mcp search > "$OUT" 2>&1
echo "agent calls finished -> $OUT"
