// Command brote-lsp serves Brote analysis over the Language Server Protocol.
package main

import (
	"fmt"
	"github.com/fclaude/brote/lsp"
	"github.com/fclaude/brote/version"
	"os"
)

func main() {
	if len(os.Args) == 2 && (os.Args[1] == "--version" || os.Args[1] == "-version") {
		fmt.Println(version.Line("brote-lsp"))
		return
	}
	if len(os.Args) != 1 {
		fmt.Fprintln(os.Stderr, "usage: brote-lsp [--version]")
		os.Exit(2)
	}
	if err := lsp.Serve(os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
