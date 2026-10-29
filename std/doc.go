// Package std is Brote's extended standard library: Go's files, network,
// processes, clock, randomness, text and encoding tools behind a policy.
//
// The scheme package is pure: a script reaches the machine only through
// primitives the host registers. Package std is the batteries a host can
// register in one call, and the policy it registers them under:
//
//	pol, _ := std.ParsePolicy(`(policy (profile safe) (fs (read "data")))`)
//	rt, err := std.Install(in, pol, std.WithBaseDir(dir))
//
// A module the policy does not grant registers nothing, so its procedures are
// unbound variables in the script, not refused calls. A module that is granted
// is scoped (these directories, these hosts, these commands) and capped (file
// size, response size, process output, time). A policy is parsed, never
// evaluated, and anything it does not understand is an error. Intersect
// narrows one policy by another, which is how a host limits a plugin to what
// it asked for and the host allows (OpenPlugin).
//
// See docs/capabilities.md for the model and docs/std.md for the procedures.
package std
