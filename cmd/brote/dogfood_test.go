package main

import "testing"

// The language's own tests are a file of Brote tests, run by the command that
// runs everyone's.
func TestTheLanguageTestsPass(t *testing.T) {
	status, out, errs := invoke(t, "", "test", "../../tests")
	if status != 0 {
		t.Fatalf("status %d\n%s%s", status, out, errs)
	}
}
