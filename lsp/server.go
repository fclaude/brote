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
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/fclaude/brote/analysis"
	"github.com/fclaude/brote/scheme"
)

const MaxMessage = 4 << 20

// maxWorkspaceFiles bounds how many files the server indexes under the
// workspace root, and maxWorkspaceFile their size.
const (
	maxWorkspaceFiles = 2000
	maxWorkspaceFile  = 1 << 20
)

type message struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}
type document struct {
	uri      string
	version  int
	text     string
	analysis *analysis.Document
	// For a file indexed from disk, what it was when read: a change on disk
	// is read again before the index is used.
	path    string
	size    int64
	modTime time.Time
}
type Server struct {
	documents             map[string]*document
	workspace             map[string]*document // indexed under the root URI, open or not
	root                  string
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
	s := &Server{documents: map[string]*document{}, workspace: map[string]*document{}, writer: out}
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
			RootURI string `json:"rootUri"`
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
		s.root = p.RootURI
		s.initialized = true
		s.indexWorkspace()
		return map[string]any{"serverInfo": map[string]any{"name": "brote-lsp"}, "capabilities": map[string]any{"positionEncoding": "utf-16", "textDocumentSync": map[string]any{"openClose": true, "change": 1}, "documentSymbolProvider": true, "definitionProvider": true, "referencesProvider": true, "workspaceSymbolProvider": true, "hoverProvider": true, "documentFormattingProvider": true, "renameProvider": true, "semanticTokensProvider": map[string]any{"legend": map[string]any{"tokenTypes": []string{"keyword", "function", "variable", "builtin"}, "tokenModifiers": []string{}}, "full": true}, "completionProvider": map[string]any{}}}, 0, nil
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
		d := &document{uri: t.URI, version: t.Version, text: t.Text, analysis: analysis.Analyze(t.Text, s.profile)}
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
		d.text = p.Changes[len(p.Changes)-1].Text
		d.analysis = analysis.Analyze(d.text, s.profile)
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
		for uri, d := range s.allDocuments() {
			for _, def := range d.analysis.Definitions {
				if strings.Contains(strings.ToLower(def.Name), strings.ToLower(p.Query)) {
					out = append(out, map[string]any{"name": def.Name, "kind": symbolKind(def), "location": location{uri, def.Range}})
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
		NewName  string            `json:"newName"`
		Context  struct {
			IncludeDeclaration bool `json:"includeDeclaration"`
		} `json:"context"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return invalid(err)
	}
	d := s.documents[p.TextDocument.URI]
	switch m.Method {
	case "textDocument/documentSymbol", "textDocument/definition", "textDocument/references", "textDocument/hover", "textDocument/completion", "textDocument/formatting", "textDocument/rename", "textDocument/semanticTokens/full":
		if d == nil {
			return invalid(errors.New("document is not open"))
		}
	default:
		return nil, -32601, fmt.Errorf("method not found: %s", m.Method)
	}
	switch m.Method {
	case "textDocument/rename":
		def := d.analysis.DefinitionAt(p.Position)
		if def == nil {
			return invalid(errors.New("no defined name at this position"))
		}
		if forms, err := scheme.Read(p.NewName); err != nil || len(forms) != 1 || forms[0].Kind != scheme.KSymbol || forms[0].S != p.NewName {
			return invalid(fmt.Errorf("newName %q is not a name", p.NewName))
		}
		changes := map[string][]any{}
		addEdits := func(uri string, ranges []analysis.Range) {
			var edits []any
			seen := map[analysis.Range]bool{}
			for _, r := range ranges {
				if !seen[r] {
					seen[r] = true
					edits = append(edits, map[string]any{"range": r, "newText": p.NewName})
				}
			}
			if len(edits) > 0 {
				changes[uri] = edits
			}
		}
		if !def.Global {
			// A parameter or local binding: its uses in this document, and
			// nothing else of the same name.
			ranges := []analysis.Range{def.Range}
			for _, r := range d.analysis.References {
				if r.Definition == def {
					ranges = append(ranges, r.Range)
				}
			}
			addEdits(p.TextDocument.URI, ranges)
			return map[string]any{"changes": changes}, 0, nil
		}
		// A top-level name: the top-level definitions of it in every indexed
		// file, and the references that resolve to one or to nothing in their
		// own file (a use of another file's definition) — never one a local
		// binding of the same name shadows.
		for uri, doc := range s.allDocuments() {
			var ranges []analysis.Range
			for _, df := range doc.analysis.Definitions {
				if df.Global && df.Name == def.Name {
					ranges = append(ranges, df.Range)
				}
			}
			for _, r := range doc.analysis.References {
				if r.Name == def.Name && (r.Definition == nil || r.Definition.Global) {
					ranges = append(ranges, r.Range)
				}
			}
			addEdits(uri, ranges)
		}
		return map[string]any{"changes": changes}, 0, nil
	case "textDocument/semanticTokens/full":
		return map[string]any{"data": semanticTokens(d.analysis)}, 0, nil
	case "textDocument/formatting":
		out, err := scheme.Format(d.text)
		if err != nil {
			return invalid(err)
		}
		if out == d.text {
			return []any{}, 0, nil
		}
		lines := strings.Split(d.text, "\n")
		end := map[string]any{"line": len(lines) - 1, "character": utf16Len(lines[len(lines)-1])}
		rng := map[string]any{"start": map[string]any{"line": 0, "character": 0}, "end": end}
		return []any{map[string]any{"range": rng, "newText": out}}, 0, nil
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

// utf16Len is the length of s in UTF-16 code units, what LSP positions count.
func utf16Len(s string) int { return len(utf16.Encode([]rune(s))) }

// allDocuments is the open documents plus every file indexed under the
// workspace root, an open document winning over the indexed copy of the same
// file however its URI is spelled. An indexed file that changed on disk is
// read again first, and one that is gone is dropped.
func (s *Server) allDocuments() map[string]*document {
	out := make(map[string]*document, len(s.workspace)+len(s.documents))
	open := make(map[string]bool, len(s.documents))
	for uri, d := range s.documents {
		out[uri] = d
		open[uriKey(uri)] = true
	}
	for uri, d := range s.workspace {
		if open[uriKey(uri)] {
			continue
		}
		fi, err := os.Stat(d.path)
		if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxWorkspaceFile {
			delete(s.workspace, uri)
			continue
		}
		if fi.Size() != d.size || !fi.ModTime().Equal(d.modTime) {
			nd := s.readWorkspaceFile(d.path)
			if nd == nil {
				delete(s.workspace, uri)
				continue
			}
			s.workspace[uri], d = nd, nd
		}
		out[uri] = d
	}
	return out
}

// uriKey is what identifies a document's file whatever escaping its URI
// uses: the cleaned path of a file: URI, or the URI itself.
func uriKey(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Scheme == "file" && u.Path != "" {
		return "file:" + filepath.Clean(u.Path)
	}
	return uri
}

// fileURI is the file: URI of an absolute path, escaped as clients send it.
func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(path)}).String()
}

// readWorkspaceFile reads and analyzes one file of the workspace, or returns
// nil when it cannot be read or is over the size limit.
func (s *Server) readWorkspaceFile(path string) *document {
	fi, err := os.Stat(path)
	if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxWorkspaceFile {
		return nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	uri := fileURI(path)
	return &document{uri: uri, text: string(data), analysis: analysis.Analyze(string(data), s.profile), path: path, size: fi.Size(), modTime: fi.ModTime()}
}

// indexWorkspace analyzes every .scm file under the root URI, bounded, so
// workspace search and rename reach files that were never opened.
func (s *Server) indexWorkspace() {
	if s.root == "" {
		return
	}
	u, err := url.Parse(s.root)
	if err != nil || u.Scheme != "file" || u.Path == "" {
		return
	}
	root := filepath.FromSlash(u.Path)
	_ = filepath.WalkDir(root, func(p string, e fs.DirEntry, err error) error {
		if err != nil {
			return nil // unreadable entries are skipped, not fatal
		}
		if e.IsDir() {
			if p != root && strings.HasPrefix(e.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if len(s.workspace) >= maxWorkspaceFiles {
			return filepath.SkipAll
		}
		if !strings.HasSuffix(p, ".scm") {
			return nil
		}
		if d := s.readWorkspaceFile(p); d != nil {
			s.workspace[d.uri] = d
		}
		return nil
	})
}

// semantic token types, in the order the legend advertises them.
const (
	tokKeyword  = 0
	tokFunction = 1
	tokVariable = 2
	tokBuiltin  = 3
)

// semanticTokens encodes a document's definitions and references as LSP
// semantic tokens: user functions and variables get their own types,
// builtins and host symbols another, delta-encoded five numbers per token.
func semanticTokens(d *analysis.Document) []int {
	type tok struct {
		rng analysis.Range
		typ int
	}
	var toks []tok
	seen := map[analysis.Range]bool{}
	for _, def := range d.Definitions {
		t := tokVariable
		if def.Function {
			t = tokFunction
		}
		toks = append(toks, tok{def.Range, t})
		seen[def.Range] = true
	}
	for _, r := range d.References {
		if seen[r.Range] {
			continue
		}
		var t int
		switch {
		case r.Definition != nil && r.Definition.Function:
			t = tokFunction
		case r.Definition != nil:
			t = tokVariable
		default:
			if _, ok := d.Symbols[r.Name]; !ok {
				continue // unknown name: no claim
			}
			t = tokBuiltin
		}
		toks = append(toks, tok{r.Range, t})
		seen[r.Range] = true
	}
	sort.SliceStable(toks, func(i, j int) bool {
		return analysis.Before(toks[i].rng.Start, toks[j].rng.Start)
	})
	var data []int
	prevLine, prevChar := 0, 0
	for _, t := range toks {
		dl := t.rng.Start.Line - prevLine
		dc := t.rng.Start.Character
		if t.rng.Start.Line == prevLine {
			dc -= prevChar
		}
		data = append(data, dl, dc, t.rng.End.Character-t.rng.Start.Character, t.typ, 0)
		prevLine, prevChar = t.rng.Start.Line, t.rng.Start.Character
	}
	return data
}
