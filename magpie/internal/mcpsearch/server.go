// Package mcpsearch serves MolaSearch to an agent over MCP's stdio
// transport: newline-delimited JSON-RPC, tools that really run the
// investigation engine. This is what `molasearch mcp` is, and what the
// Detective page's agent injection points an agent at.
package mcpsearch

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"sync"

	"github.com/yetone/magpie/internal/searchdet"
)

// Run is `magpie mcp search`: the stdio server until the agent closes the
// pipe or the process is interrupted.
func Run(args []string) error {
	if len(args) > 0 {
		return fmt.Errorf("unknown search MCP server %q (there is none to name)", args[0])
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()
	return Serve(ctx, os.Stdin, os.Stdout)
}

// Serve runs the stdio loop until in ends or the context is cancelled.
func Serve(ctx context.Context, in io.Reader, out io.Writer) error {
	if in == nil {
		in = os.Stdin
	}
	if out == nil {
		out = os.Stdout
	}
	s := &server{engine: searchdet.NewEngine(), enc: json.NewEncoder(out), mu: &sync.Mutex{}, origin: "MCP client"}
	sc := bufio.NewScanner(in)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		s.handleLine(ctx, line)
	}
	return sc.Err()
}

type server struct {
	engine *searchdet.Engine
	enc    *json.Encoder
	mu     *sync.Mutex
	// origin is the MCP client's own name, learned from initialize's
	// clientInfo. Investigations carry it so the proof tab can say which
	// agent asked, instead of every run looking like the page's own.
	origin string
}

type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// reply writes one message; the mutex keeps concurrent tool calls from
// interleaving their lines.
func (s *server) reply(v rpcResponse) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := s.enc.Encode(v); err != nil {
		log.Printf("mcpsearch: write: %v", err)
	}
}

func (s *server) handleLine(ctx context.Context, line []byte) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		s.reply(rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}})
		return
	}
	switch req.Method {
	case "initialize":
		var in struct {
			ClientInfo struct {
				Name    string `json:"name"`
				Version string `json:"version"`
			} `json:"clientInfo"`
		}
		if len(req.Params) > 0 && json.Unmarshal(req.Params, &in) == nil {
			name := strings.TrimSpace(in.ClientInfo.Name)
			version := strings.TrimSpace(in.ClientInfo.Version)
			switch {
			case name != "" && version != "":
				s.origin = "MCP/" + name + " " + version
			case name != "":
				s.origin = "MCP/" + name
			}
		}
		s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{"tools": map[string]any{}},
			"serverInfo":      map[string]any{"name": "molasearch", "version": "5.0"},
		}})
	case "notifications/initialized", "notifications/cancelled":
		// no reply to a notification
	case "ping":
		s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}})
	case "tools/list":
		s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{"tools": toolList()}})
	case "tools/call":
		s.callTool(ctx, req)
	default:
		if req.ID != nil {
			s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32601, Message: "method not found: " + req.Method}})
		}
	}
}

// toolSpec is one MCP tool's declaration.
type toolSpec struct {
	Name        string         `json:"name"`
	Description string         `json:"description"`
	InputSchema map[string]any `json:"inputSchema"`
}

// toolList is what the gateway offers an agent.
func toolList() []toolSpec {
	query := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query":  map[string]any{"type": "string", "description": "要查证的问题"},
			"modeId": map[string]any{"type": "string", "description": "M01~M30，默认 M11"},
		},
		"required": []string{"query"},
	}
	fast := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{"type": "string", "description": "要搜索的问题"},
		},
		"required": []string{"query"},
	}
	return []toolSpec{
		{Name: "magpie_investigate", Description: "多源联网查证：返回判定（VERIFIED/REFUTED/UNVERIFIED/ABSTAIN）、带真实 URL 的引用、时间树与证据信封。查不到时判定为 ABSTAIN，请如实告知用户。", InputSchema: query},
		{Name: "magpie_search_fast", Description: "快速直通搜索（M01），返回真实引用列表。", InputSchema: fast},
		{Name: "magpie_debate", Description: "正反方对抗检索（M13），返回支持与反驳双方证据及裁决。", InputSchema: fast},
		{Name: "magpie_timeline", Description: "时间演化检索（M17），返回按真实日期排列的事件树。", InputSchema: fast},
	}
}

func (s *server) callTool(ctx context.Context, req rpcRequest) {
	var in struct {
		Name      string `json:"name"`
		Arguments struct {
			Query  string `json:"query"`
			ModeID string `json:"modeId"`
		} `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &in); err != nil {
		s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "bad params"}})
		return
	}
	mode := in.Arguments.ModeID
	switch in.Name {
	case "magpie_search_fast":
		mode = "M01"
	case "magpie_debate":
		mode = "M13"
	case "magpie_timeline":
		mode = "M17"
	case "magpie_investigate":
		if mode == "" {
			mode = "M11"
		}
	}
	if in.Arguments.Query == "" {
		s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Error: &rpcError{Code: -32602, Message: "query is required"}})
		return
	}
	cctx, cancel := context.WithCancel(ctx)
	defer cancel()
	inv := s.engine.Investigate(cctx, searchdet.Options{
		Query: in.Arguments.Query, ModeID: mode, Origin: s.origin,
	})
	// The envelope is what the agent reads; the full answer goes beside it
	// as structured content for a client that wants it.
	text := inv.AgentEnvelopeXML
	if text == "" {
		text = fmt.Sprintf("verdict=%s citations=%d", inv.Verdict, len(inv.Citations))
	}
	s.reply(rpcResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"structuredContent": map[string]any{
			"verdict": inv.Verdict, "trustScore": inv.TrustScore,
			"originAgent": inv.OriginAgent,
			"citations":   inv.Citations, "temporalTree": inv.TemporalTree,
			"abstainReason": inv.AbstainReason,
		},
		"isError": inv.Verdict == "ABSTAIN" && len(inv.Citations) == 0,
	}})
}
