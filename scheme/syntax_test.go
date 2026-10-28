package scheme

import "testing"

func TestSyntaxRangesAndRecovery(t *testing.T) {
	src := "; hi\n(define café \"😀\") (define (f x) (+ x"
	nodes, err := ReadSyntax(src)
	if err == nil || len(nodes) != 2 {
		t.Fatal(nodes, err)
	}
	ident := nodes[0].Children[1]
	if src[ident.Start:ident.End] != "café" || ident.Atom != "café" {
		t.Fatal(ident)
	}
	if nodes[1].End != len(src) || nodes[1].Children[1].Children[0].Atom != "f" {
		t.Fatal("incomplete syntax lost")
	}
}
func FuzzReadSyntax(f *testing.F) {
	for _, s := range []string{"(define (x y) y)", "'(a . b)", "#(1 2)", "#| open", ",@x", "(a \\", "((", "(define [x] 1)"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		if len(s) > 8192 {
			t.Skip()
		}
		nodes, _ := ReadSyntax(s)
		for _, n := range nodes {
			if n.Start < 0 || n.End > len(s) || n.Start > n.End {
				t.Fatal(n)
			}
		}
	})
}
