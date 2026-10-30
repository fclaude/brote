package lsp

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/fclaude/brote/scheme"
)

func TestSession(t *testing.T) {
	messages := []string{
		`{"id":1,"method":"initialize","params":{"initializationOptions":{"hostProfile":{"name":"test","symbols":[{"name":"message","doc":"Show it"}]}}}}`,
		`{"method":"initialized"}`,
		`{"method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///init.scm","version":1,"text":"(define x 1) x (message x)"}}}`,
		`{"id":2,"method":"textDocument/definition","params":{"textDocument":{"uri":"file:///init.scm"},"position":{"line":0,"character":13}}}`,
		`{"id":3,"method":"textDocument/references","params":{"textDocument":{"uri":"file:///init.scm"},"position":{"line":0,"character":13},"context":{"includeDeclaration":true}}}`,
		`{"id":4,"method":"textDocument/hover","params":{"textDocument":{"uri":"file:///init.scm"},"position":{"line":0,"character":16}}}`,
		`{"method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///init.scm","version":2},"contentChanges":[{"text":"(define x"}]}}`,
		`{"method":"textDocument/didChange","params":{"textDocument":{"uri":"file:///init.scm","version":1},"contentChanges":[{"text":"stale"}]}}`,
		`{"id":5,"method":"textDocument/documentSymbol","params":{"textDocument":{"uri":"file:///init.scm"}}}`,
		`{"id":6,"method":"unknown","params":{}}`,
		`{"method":"textDocument/didClose","params":{"textDocument":{"uri":"file:///init.scm"}}}`,
		`{"id":7,"method":"shutdown"}`, `{"method":"exit"}`,
	}
	var in, out bytes.Buffer
	for _, m := range messages {
		fmt.Fprintf(&in, "Content-Length: %d\r\n\r\n%s", len(m), m)
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&out)
	replies := map[int]map[string]any{}
	diagnostics := 0
	for {
		data, err := readFrame(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if id, ok := m["id"].(float64); ok {
			replies[int(id)] = m
		} else {
			diagnostics++
		}
	}
	if diagnostics != 3 {
		t.Fatal("diagnostic publications", diagnostics)
	}
	if len(replies[2]["result"].([]any)) != 1 || len(replies[3]["result"].([]any)) != 3 {
		t.Fatal(replies)
	}
	if !strings.Contains(fmt.Sprint(replies[4]), "Show it") {
		t.Fatal(replies[4])
	}
	if len(replies[5]["result"].([]any)) != 1 {
		t.Fatal("stale version accepted")
	}
	if replies[6]["error"] == nil {
		t.Fatal("missing method error")
	}
}
func TestFramingBounds(t *testing.T) {
	for _, frame := range []string{"Content-Length: -1\r\n\r\n", "Content-Length: 99999999\r\n\r\n", "Content-Length: 1\r\nContent-Length: 1\r\n\r\nx"} {
		if _, err := readFrame(bufio.NewReader(strings.NewReader(frame))); err == nil {
			t.Fatal(frame)
		}
	}
}

func TestFormatting(t *testing.T) {
	bad := "(define (f x)\n(+ x 1))\n"
	text, _ := json.Marshal(bad)
	messages := []string{
		`{"id":1,"method":"initialize","params":{"initializationOptions":{"hostProfile":{"name":"t","symbols":[]}}}}`,
		`{"method":"initialized"}`,
		fmt.Sprintf(`{"method":"textDocument/didOpen","params":{"textDocument":{"uri":"file:///a.scm","version":1,"text":%s}}}`, text),
		`{"id":2,"method":"textDocument/formatting","params":{"textDocument":{"uri":"file:///a.scm"}}}`,
		`{"id":3,"method":"shutdown"}`, `{"method":"exit"}`,
	}
	var in, out bytes.Buffer
	for _, m := range messages {
		fmt.Fprintf(&in, "Content-Length: %d\r\n\r\n%s", len(m), m)
	}
	if err := Serve(&in, &out); err != nil {
		t.Fatal(err)
	}
	r := bufio.NewReader(&out)
	replies := map[int]map[string]any{}
	for {
		data, err := readFrame(r)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatal(err)
		}
		if id, ok := m["id"].(float64); ok {
			replies[int(id)] = m
		}
	}
	caps := replies[1]["result"].(map[string]any)["capabilities"].(map[string]any)
	if caps["documentFormattingProvider"] != true {
		t.Errorf("initialize capabilities do not advertise formatting: %v", caps)
	}
	edits, ok := replies[2]["result"].([]any)
	if !ok || len(edits) != 1 {
		t.Fatalf("formatting result: %v", replies[2])
	}
	edit := edits[0].(map[string]any)
	want, err := scheme.Format(bad)
	if err != nil {
		t.Fatal(err)
	}
	if edit["newText"] != want {
		t.Errorf("newText = %q, want %q", edit["newText"], want)
	}
	rng := edit["range"].(map[string]any)
	if rng["start"].(map[string]any)["line"] != float64(0) || rng["end"].(map[string]any)["line"] != float64(2) {
		t.Errorf("the edit does not span the document: %v", rng)
	}
}
