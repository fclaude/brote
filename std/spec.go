package std

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fclaude/brote/scheme"
)

// ApplySpec grants one thing written as a flag value, for a command line:
//
//	regexp                  an unscoped module, by name
//	fs.read=data,/etc/hosts directory trees to read      (fs.write=... to write)
//	net=api.x.com,*.y.com   hosts to reach               (net.allow, the same)
//	net.methods=get,post    methods besides get and head
//	net.private=true        let requests reach private and local addresses
//	exec=git,/usr/bin/jq    commands to run              (exec.allow, the same)
//	exec.env=PATH,HOME      variables the commands see
//	env=HOME,LANG           variables scripts can read   (env.allow, the same)
//
// A value of * means everything. Lists are separated by commas.
func (p *Policy) ApplySpec(spec string) error {
	key, value, hasValue := strings.Cut(spec, "=")
	module, field, _ := strings.Cut(key, ".")
	if !slices.Contains(Modules, module) {
		return fmt.Errorf("unknown module %q in %q (modules: %s)", module, spec, strings.Join(Modules, ", "))
	}
	if p.Modules == nil {
		p.Modules = map[string]bool{}
	}
	var list []string
	var anyValue bool
	if hasValue {
		for _, v := range strings.Split(value, ",") {
			if v = strings.TrimSpace(v); v == "*" {
				anyValue = true
			} else if v != "" {
				list = append(list, v)
			}
		}
		if anyValue && len(list) > 0 {
			return fmt.Errorf("%q: * cannot be mixed with other entries", spec)
		}
	}
	need := func(what string) error {
		if !hasValue || (len(list) == 0 && !anyValue) {
			return fmt.Errorf("%q needs a value, for example %s", spec, what)
		}
		return nil
	}
	if scoped[module] && field == "" && !hasValue {
		return fmt.Errorf("%q needs a value, for example %s", spec, map[string]string{Env: "env=HOME", FS: "fs.read=data", Net: "net=api.example.com", Exec: "exec=git"}[module])
	}
	switch {
	case !scoped[module]:
		if field != "" || hasValue {
			return fmt.Errorf("%q: module %s takes no scope", spec, module)
		}
		p.Modules[module] = true
	case module == Env && (field == "" || field == "allow"):
		if err := need("env=HOME,LANG"); err != nil {
			return err
		}
		p.Modules[Env] = true
		p.Env.Any = p.Env.Any || anyValue
		p.Env.Allow = append(p.Env.Allow, list...)
	case module == FS && (field == "read" || field == "write"):
		if err := need("fs." + field + "=data"); err != nil {
			return err
		}
		p.Modules[FS] = true
		switch {
		case field == "read" && anyValue:
			p.FS.AnyRead = true
		case field == "write" && anyValue:
			p.FS.AnyWrite = true
		case field == "read":
			p.FS.Read = append(p.FS.Read, list...)
		default:
			p.FS.Write = append(p.FS.Write, list...)
		}
	case module == Net && (field == "" || field == "allow"):
		if err := need("net=api.example.com"); err != nil {
			return err
		}
		p.Modules[Net] = true
		p.Net.Any = p.Net.Any || anyValue
		for _, h := range list {
			c, err := canonHostPattern(h)
			if err != nil {
				return err
			}
			p.Net.Allow = append(p.Net.Allow, c)
		}
	case module == Net && field == "methods":
		if err := need("net.methods=get,post"); err != nil {
			return err
		}
		if anyValue {
			list = allMethods
		}
		for _, m := range list {
			if m = strings.ToLower(m); !slices.Contains(allMethods, m) {
				return fmt.Errorf("%q: unknown method %q", spec, m)
			}
			p.Net.Methods = append(p.Net.Methods, strings.ToLower(m))
		}
		p.Modules[Net] = true
	case module == Net && field == "private":
		switch value {
		case "true":
			p.Net.Private = true
		case "false":
			p.Net.Private = false
		default:
			return fmt.Errorf("%q: use net.private=true or net.private=false", spec)
		}
		p.Modules[Net] = true
	case module == Exec && (field == "" || field == "allow"):
		if err := need("exec=git"); err != nil {
			return err
		}
		for _, c := range list {
			if strings.ContainsAny(c, `/\`) && !filepath.IsAbs(c) {
				return fmt.Errorf("command %q has a directory: give an absolute path or a bare name", c)
			}
		}
		p.Modules[Exec] = true
		p.Exec.Any = p.Exec.Any || anyValue
		p.Exec.Allow = append(p.Exec.Allow, list...)
	case module == Exec && field == "env":
		if err := need("exec.env=PATH"); err != nil {
			return err
		}
		p.Modules[Exec] = true
		p.Exec.Env.Any = p.Exec.Env.Any || anyValue
		p.Exec.Env.Allow = append(p.Exec.Env.Allow, list...)
	default:
		return fmt.Errorf("%q: %s has no field %q", spec, module, field)
	}
	p.normalize()
	return nil
}

// Deny removes a module from the policy.
func (p *Policy) Deny(module string) error {
	if !slices.Contains(Modules, module) {
		return fmt.Errorf("unknown module %q (modules: %s)", module, strings.Join(Modules, ", "))
	}
	delete(p.Modules, module)
	p.normalize()
	return nil
}

// Pretty prints the policy one module to a line, in the syntax ParsePolicy reads.
func (p *Policy) Pretty() string {
	var sb strings.Builder
	sb.WriteString("(policy")
	for _, m := range p.Granted() {
		sb.WriteString("\n  " + scheme.Write(p.clause(m)))
	}
	sb.WriteString(")")
	return sb.String()
}
