package main

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/fclaude/brote/std"
)

// listFlag is a flag that may be given more than once.
type listFlag []string

func (l *listFlag) String() string     { return fmt.Sprint([]string(*l)) }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

// buildPolicy decides what the script can reach (docs/capabilities.md).
//
// With no flags the command runs the person's own script with the person's own
// rights, so it grants every module unscoped. -sandbox starts from the safe
// profile instead (no files, network, processes or environment); -policy FILE
// starts from the policy in FILE. -allow adds to a restricted start, and
// naming anything with -allow switches the start from everything to the safe
// profile, so that `-allow fs.read=data` means data and nothing else. -deny
// removes a module from whatever the start was.
func buildPolicy(sandbox bool, policyFile string, allow, deny []string) (*std.Policy, error) {
	if sandbox && policyFile != "" {
		return nil, fmt.Errorf("-sandbox and -policy both choose where the grant starts: use one")
	}
	var p *std.Policy
	switch {
	case policyFile != "":
		src, err := os.ReadFile(policyFile)
		if err != nil {
			return nil, err
		}
		if p, err = std.ParsePolicy(string(src)); err != nil {
			return nil, fmt.Errorf("%s: %w", policyFile, err)
		}
		// Directories in a policy file mean the directory the file is in.
		p = p.Resolve(filepath.Dir(policyFile))
	case sandbox || len(allow) > 0:
		p = std.Allow(std.Profile("safe"))
	default:
		p = std.Full()
	}
	for _, spec := range allow {
		if err := p.ApplySpec(spec); err != nil {
			return nil, fmt.Errorf("-allow: %w", err)
		}
	}
	for _, m := range deny {
		if err := p.Deny(m); err != nil {
			return nil, fmt.Errorf("-deny: %w", err)
		}
	}
	return p, nil
}
