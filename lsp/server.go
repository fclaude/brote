// Package lsp implements Brote's stdio language server. Documents are analyzed
// in memory; neither source code nor host-profile handlers are executed.
package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/fclaude/brote/analysis"
)

const MaxMessage = 4 << 20

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type document struct {
	uri      string
	version  int
	analysis *analysis.Document
}
type Server struct {
	documents             map[string]*document
	profile               analysis.Profile
	initialized, shutdown bool
	writer                io.Writer
}
type location struct {
	URI   string         `json:"uri"`
	Range analysis.Range `json:"range"`
}
type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// Serve runs until exit or EOF. Messages and documents are bounded. All writes
// are serialized on this goroutine, keeping stdout strictly LSP-framed JSON.
func Serve(in io.Reader, out io.Writer) error {
	s := &Server{documents: map[string]*document{}, writer: out}
	r := bufio.NewReader(in)
	for {
		data, err := readFrame(r)
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		var m message
		if err = json.Unmarshal(data, &m); err != nil {
			if e := s.write(map[string]any{"id": nil, "error": rpcError{-32700, "invalid JSON"}}); e != nil {
				return e
			}
			continue
		}
		if m.Method == "exit" {
			if !s.shutdown {
				return errors.New("exit before shutdown")
			}
			return nil
		}
		result, code, err := s.handle(m)
		if len(m.ID) == 0 {
			if err != nil && code == 0 {
				return err
			}
			continue
		}
		reply := map[string]any{"id": m.ID, "result": result}
		if err != nil {
			delete(reply, "result")
			reply["error"] = rpcError{code, err.Error()}
		}
		if err := s.write(reply); err != nil {
			return err
		}
	}
}
func readFrame(r *bufio.Reader) ([]byte, error) {
	size := -1
	header := 0
	for {
		rawLine, err := r.ReadSlice('\n')
		line := string(rawLine)
		if err != nil {
			return nil, err
		}
		header += len(line)
		if header > 8192 {
			return nil, errors.New("LSP header too large")
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return nil, errors.New("invalid LSP header")
		}
		if strings.EqualFold(key, "Content-Length") {
			if size != -1 {
				return nil, errors.New("duplicate Content-Length")
			}
			n, e := strconv.Atoi(strings.TrimSpace(value))
			if e != nil || n < 0 || n > MaxMessage {
				return nil, errors.New("invalid Content-Length")
			}
			size = n
		}
	}
	if size < 0 {
		return nil, errors.New("missing Content-Length")
	}
	data := make([]byte, size)
	_, err := io.ReadFull(r, data)
	return data, err
}
func (s *Server) write(m map[string]any) error {
	m["jsonrpc"] = "2.0"
	data, err := json.Marshal(m)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(s.writer, "Content-Length: %d\r\n\r\n%s", len(data), data)
	return err
}
func (s *Server) publish(d *document) error {
	return s.write(map[string]any{"method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": d.uri, "version": d.version, "diagnostics": d.analysis.Diagnostics}})
}
func invalid(err error) (any, int, error) { return nil, -32602, err }
func (s *Server) handle(m message) (any, int, error) {
	if s.shutdown {
		return nil, -32600, errors.New("server has shut down")
	}
	if m.Method == "initialize" {
		if s.initialized {
			return nil, -32600, errors.New("already initialized")
		}
		var p struct {
			Options struct {
				HostProfile analysis.Profile `json:"hostProfile"`
			} `json:"initializationOptions"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return invalid(err)
		}
		if err := analysis.ValidateProfile(p.Options.HostProfile); err != nil {
			return invalid(err)
		}
		s.profile = p.Options.HostProfile
		s.initialized = true
		return map[string]any{"serverInfo": map[string]any{"name": "brote-lsp"}, "capabilities": map[string]any{"positionEncoding": "utf-16", "textDocumentSync": map[string]any{"openClose": true, "change": 1}, "documentSymbolProvider": true, "definitionProvider": true, "referencesProvider": true, "workspaceSymbolProvider": true, "hoverProvider": true, "completionProvider": map[string]any{}}}, 0, nil
	}
	if !s.initialized {
		return nil, -32002, errors.New("server not initialized")
	}
	switch m.Method {
	case "initialized", "$/cancelRequest", "$/setTrace", "textDocument/didSave":
		return nil, 0, nil
	case "shutdown":
		s.shutdown = true
		return nil, 0, nil
	case "textDocument/didOpen":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Text    string `json:"text"`
				Version int    `json:"version"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return invalid(err)
		}
		t := p.TextDocument
		if t.URI == "" {
			return invalid(errors.New("missing document URI"))
		}
		d := &document{t.URI, t.Version, analysis.Analyze(t.Text, s.profile)}
		s.documents[t.URI] = d
		return nil, 0, s.publish(d)
	case "textDocument/didChange":
		var p struct {
			TextDocument struct {
				URI     string `json:"uri"`
				Version int    `json:"version"`
			} `json:"textDocument"`
			Changes []struct {
				Text  string          `json:"text"`
				Range json.RawMessage `json:"range"`
			} `json:"contentChanges"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return invalid(err)
		}
		d := s.documents[p.TextDocument.URI]
		if d == nil || p.TextDocument.Version <= d.version {
			return nil, 0, nil
		}
		if len(p.Changes) == 0 {
			return invalid(errors.New("missing full document change"))
		}
		for _, c := range p.Changes {
			if len(c.Range) > 0 && !bytes.Equal(c.Range, []byte("null")) {
				return invalid(errors.New("only full document sync supported"))
			}
		}
		d.version = p.TextDocument.Version
		d.analysis = analysis.Analyze(p.Changes[len(p.Changes)-1].Text, s.profile)
		return nil, 0, s.publish(d)
	case "textDocument/didClose":
		var p struct {
			TextDocument struct {
				URI string `json:"uri"`
			} `json:"textDocument"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return invalid(err)
		}
		delete(s.documents, p.TextDocument.URI)
		return nil, 0, s.write(map[string]any{"method": "textDocument/publishDiagnostics", "params": map[string]any{"uri": p.TextDocument.URI, "diagnostics": []any{}}})
	case "workspace/symbol":
		var p struct {
			Query string `json:"query"`
		}
		if err := json.Unmarshal(m.Params, &p); err != nil {
			return invalid(err)
		}
		out := []any{}
		for _, d := range s.documents {
			for _, def := range d.analysis.Definitions {
				if strings.Contains(strings.ToLower(def.Name), strings.ToLower(p.Query)) {
					out = append(out, map[string]any{"name": def.Name, "kind": symbolKind(def), "location": location{d.uri, def.Range}})
				}
			}
		}
		return out, 0, nil
	}
	var p struct {
		TextDocument struct {
			URI string `json:"uri"`
		} `json:"textDocument"`
		Position analysis.Position `json:"position"`
		Context  struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return invalid(err)
	}
	d := s.documents[p.TextDocument.URI]
	switch m.Method {
	case "textDocument/documentSymbol", "textDocument/definition", "textDocument/references", "textDocument/hover", "textDocument/completion":
		if d == nil {
			return invalid(errors.New("document is not open"))
		}
	default:
		return nil, -32601, fmt.Errorf("method not found: %s", m.Method)
	}
	switch m.Method {
	case "textDocument/documentSymbol":
		out := []any{}
		for _, def := range d.analysis.Definitions {
			out = append(out, map[string]any{"name": def.Name, "kind": symbolKind(def), "range": def.Range, "selectionRange": def.Range})
		}
		return out, 0, nil
	case "textDocument/definition":
		out := []location{}
		if def := d.analysis.DefinitionAt(p.Position); def != nil {
			out = append(out, location{d.uri, def.Range})
		}
		return out, 0, nil
	case "textDocument/references":
		out := []location{}
		for _, rng := range d.analysis.ReferencesAt(p.Position, p.Context.IncludeDeclaration) {
			out = append(out, location{d.uri, rng})
		}
		return out, 0, nil
	case "textDocument/hover":
		text := d.analysis.Hover(p.Position)
		if text == "" {
			return nil, 0, nil
		}
		return map[string]any{"contents": map[string]any{"kind": "plaintext", "value": text}}, 0, nil
	case "textDocument/completion":
		out := []any{}
		for _, symbol := range d.analysis.Completions(p.Position) {
			out = append(out, map[string]any{"label": symbol.Name, "kind": 3, "detail": symbol.Signature(), "documentation": symbol.Doc})
		}
		return out, 0, nil
	}
	return nil, 0, nil
}
func symbolKind(d *analysis.Definition) int {
	if d.Function {
		return 12
	}
	return 13
}
