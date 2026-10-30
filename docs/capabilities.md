# Capabilities: what a script may reach

A Brote program can only touch the machine through procedures its host registered. The [extended library](std.md) gives a host a lot of procedures to register: files, HTTP, programs, the environment. This page is about letting a host choose among them, and choose how much of each, so that the same library serves a text editor that runs a user's commands, a service that runs plugins from strangers, and a person running their own script at a prompt.

The mechanism is a **policy**. A policy says which modules a script gets and, for the ones that reach outside the process, exactly what they reach. Everything not in the policy does not exist.

## Four rules

1. **Absent, not denied.** A module the policy does not grant registers no procedures. A script that calls `fs-write-text` under a policy without `fs` gets `unbound variable`, the same as for a name it made up. There is nothing to probe, shadow or find a way around.
2. **Scoped, not just on.** `fs` is granted for particular directory trees, `net` for particular hosts, `exec` for particular commands, `env` for particular variables. The scope is part of the grant and cannot be widened from inside.
3. **Fail closed.** The zero policy grants nothing. A policy that names a module, field or value Brote does not know is an error, not a shrug. A scoped module with an empty scope is an error.
4. **Only narrower.** `Intersect` combines two policies into one that is within both. A plugin that asks for more than its host holds does not get more.

A policy is data. It is read, never evaluated, so reading one (or a plugin's request) cannot run anything.

## Granting from Go

```go
in := scheme.New(scheme.WithOutput(w), scheme.WithBaseDir(dir))

pol, err := std.ParsePolicy(`(policy
  (profile safe)
  (fs   (read "notes") (write "out"))
  (net  (allow "api.example.com") (methods get post) (timeout 10s)))`)
// or in Go:
//   std.Allow(std.Profile("safe"), std.FSRead("notes"), std.FSWrite("out"),
//             std.NetHosts("api.example.com"))

rt, err := std.Install(in, pol, std.WithBaseDir(dir), std.WithAudit(logEvent))
if err != nil { /* discard in */ }
defer rt.Close()

in.SetGate(rt.Gate(nil)) // keeps the core builtin load inside the fs grant
in.Freeze()              // no more registration; register-tool stops working
v, err := in.EvalString(ctx, untrustedSource)
```

`Install` registers the procedures of the granted modules plus `require`, `modules` and `module-info`. Call it once per interpreter and before `Freeze`. Pass the same directory to `scheme.WithBaseDir` and `std.WithBaseDir`: it is what relative paths in the policy and in the script mean.

`Gate` is not optional for code you do not trust. Brote's core has one builtin that reads the disk, `load`, and `std` cannot remove it. `rt.Gate(next)` returns a gate that lets `load` read only what `fs-read-text` could (nothing, when `fs` is not granted) and hands every other call to `next`, which may be `nil` or your own gate. Everything the library does also carries the metadata a gate reads (`Host`, `Egress`, `Irreversible`, parameter roles) and the provenance taints `external:file`, `external:http`, `external:shell` and `external:env`, so a gate you already have keeps working.

`std.WithAudit` calls a function for each file written, removed, renamed or created, each HTTP request and each program run, with the error if it failed. It runs whether or not there is a gate.

**One interpreter, callers with different grants.** Install the widest policy any caller may have, and narrow it per caller. For the network that is `std.WithNetCheck`, which the net module asks at every step after the policy has allowed it (the request, each redirect hop, and each connection with the address it is about to reach), with the evaluation's context, so it knows the caller:

```go
rt, err := std.Install(in, widest, std.WithNetCheck(func(ctx context.Context, ev std.NetEvent) error {
    return policyOf(ctx).CheckNetEvent(ev) // the calling module's *std.Policy
}))
```

It can only narrow. For the rest a gate checks whether the caller has the module (`rt.ModuleOf`) and, for `proc-run`, the command (`CheckExec`). Checking only `http-request`'s arguments in a gate is not enough: a gate does not see where a redirect leads or what address a name resolves to. [Embedding](embedding.md#different-policies-for-different-callers) has the whole pattern.

## The policy language

A policy is `(policy clause …)`. A clause is a module name with optional fields. A `(profile NAME)` clause adds a set of modules and may appear anywhere; a clause for a module replaces what the profile gave it. A module appears once.

| Clause | Grants | Fields |
|---|---|---|
| `(profile pure)` | `path` `encoding` `regexp` `digest`: deterministic, no clock, nothing outside the process | |
| `(profile safe)` | `pure` plus `time` and `rand` | |
| `(profile full)` | every module, unscoped | |
| `(path)` `(encoding)` `(regexp)` `(rand)` | the module | none |
| `(digest)` `(digest (keys HEX …))` | the digests, `hmac-sha256`, and `ed25519-`/`ecdsa-` sign and verify | `(keys HEX …)` names the public keys, hex, that `…-verify` accepts, or `*` for any. Without it the module hashes and signs but verifies nothing: an unnamed key is an error, not a `#f` |
| `(time)` | the module | `(max-sleep D)`, default 10s |
| `(env …)` | `env-get`, `env-list` | `(allow NAME …)` or `(allow *)` |
| `(fs …)` | the `fs-` procedures | `(read PATH …)` `(write PATH …)` `(max-file SIZE)`, default 16MB. `*` means the whole file system. Write includes read |
| `(net …)` | `http-request`, `serve` and friends | `(allow HOST …)` `(methods M …)` `(private #t/#f)` `(max-body SIZE)`, default 5MB. `(timeout D)`, default 30s. The allow list also names the hosts `serve` may bind, besides loopback, which any net grant allows |
| `(exec …)` | `proc-run`, `proc-output` | `(allow COMMAND …)` `(timeout D)`, default 30s. `(max-output SIZE)`, default 1MB. `(env (allow NAME …))`: variables the program sees, default none |

Sizes are a number of bytes or text such as `512KB`, `16MB`, `1GB`. Durations are seconds or text such as `500ms`, `10s`, `2m`. `*` is a symbol and means everything.

**Paths** are directory trees. A relative one is taken against the base directory.

**Hosts** are `host`, `host:port` or `*.suffix`, lower case, without a scheme or a path. A pattern with no port means the scheme's own: 80 for `http`, 443 for `https`. `*.example.com` covers `api.example.com` and `a.b.example.com` but not `example.com`; name that too if you mean it. IPv6 addresses are written in brackets. A host that is not ASCII must be written in its `xn--` form.

**Methods** are `get`, `head`, `post`, `put`, `patch`, `delete`, `options`. Without `(methods …)`, only `get` and `head`: reading the network is a smaller grant than writing to it.

**Commands** are a bare name, found on the `PATH` when the policy is installed, or an absolute path. A script names the command the same way, or by the absolute path it resolved to.

`policy.String()` and `policy.Pretty()` print a policy in this syntax, and parsing the result gives back an equal policy.

## What each scope protects, and what it does not

**fs.** Each granted tree is opened as a Go [`os.Root`](https://pkg.go.dev/os#Root), and every operation under it goes through that handle. The operating system then refuses a `..` or a symbolic link, absolute or relative, that leaves the tree, whatever the script spelled and whenever the link was made. Paths are also matched to a tree before the handle is used, so a path in no tree is refused with a clearer message. Reads and writes above the size limit fail. Removing is limited to a file or an empty directory. Things to know: a hard link inside a tree to a file outside it reads and writes that file (hard links cannot be told from files); a read of a named pipe or a device blocks until it has data, and the time limit of the evaluation applies only between Scheme steps; and with `*` there is no tree to hold anything in, so `(fs (read *))` is the same as the person's own read access.

**net.** The check happens three times. Before the request, the URL's scheme, host, port and method are compared with the policy, and a URL with a user name or password, a non-ASCII host, or a scheme other than `http` and `https` is refused. On each redirect, the new URL is compared the same way, and headers the script set are dropped when the host changes. At connection time, the address the name resolved to is checked, immediately before the socket connects, and a loopback, private, link-local (where cloud metadata lives), multicast, unspecified or reserved address is refused unless the policy says `(private #t)`. That last check is what defeats a name that resolves to `127.0.0.1` and a name whose answer changes between the check and the connection; it applies even to a host the policy names. A host's `std.WithNetCheck` is asked at the same three points, after the policy, with the caller's context, so a caller with a narrower grant than the installed policy is held to it at every hop and every connection, not only the first request; while it is installed, a request's connections are its own and never carry another caller's request. Environment proxy variables are ignored. What this does not do: an allowed host can receive anything the script sends it, so a script that can read a file and call `api.example.com` can send that file there. Hosts and files are granted separately, and the combination is yours to judge. The allowlist is by name, so a name whose DNS an attacker controls is an attacker's host. **`serve`** binds a listener instead of dialing out: a loopback address or `localhost` is allowed under any net grant (it exposes nothing outside the machine), and any other bind host must match the `(allow …)` list. Loopback is judged on the address, so `127.example.com` is a name like any other. `std.WithNetCheck` is asked before the bind too, with the caller's context. Requests are answered within `max-body` and `timeout`, and handler calls run under the caller's context, so the per-caller narrowing above applies to the requests they make. A server runs until `server-stop!` or the host's `Runtime.Close`.

**exec.** A program is started with the file the policy resolved, never through a shell, with a cleaned environment and in the base directory. A script cannot choose the path it runs, the environment it gets, or its own working directory. A timeout stops the program and, on Unix, the processes it started. What this does not do: a granted program is trusted with what it can do. `git` can be told to run hooks and aliases, `find` has `-exec`, `tar` writes where it likes, and an interpreter or `sh` or `env` runs anything. Grant the narrowest program that does the job, and if it takes a script as an argument, think of that argument as a program too. A program is not confined to the `fs` grant.

**env.** The names are an allow list. A program started by `exec` gets its own list, so reading `HOME` and passing it on are separate grants.

**Not metered.** Brote counts neither memory nor CPU. A script can build a very large list within its time, and a regular expression over a very large string takes time proportional to it. The deadline on the evaluation's context bounds time; a host that runs code it does not trust bounds memory with the process (a container, `ulimit`, a cgroup). See [SECURITY.md](../SECURITY.md).

## Plugins

A plugin is a Scheme file that begins with a manifest and says what it needs:

```scheme
(plugin
  (name "word-count")
  (version "1.2")
  (description "Counts the words in your notes")
  (requires (fs (read "notes"))
            (net (allow "api.example.com"))))

(require 'fs)
(define (count-words path) (length (string-split (fs-read-text path) " ")))
```

The host decides what it is given:

```go
p, err := std.OpenPlugin(ctx, "plugins/word-count.scm", hostPolicy,
    std.OnExcess(std.ExcessDegrade),     // or ExcessFail, the default
    std.PluginOutput(w),
    std.PluginSetup(registerMyAPI))      // the host's own procedures for plugins
if err != nil { … }
defer p.Close()

n, err := p.Call(ctx, "count-words", scheme.Str("notes/today.txt"))
```

`OpenPlugin`:

1. reads the manifest as data, without running anything;
2. takes the **intersection** of the host policy and what the plugin `requires`, with the plugin's relative paths meaning the plugin file's own directory;
3. if the plugin asked for more than the host has, returns a `*std.ExcessError` listing it (`ExcessFail`), or goes on with the smaller grant and records what it refused in `Plugin.Refused` (`ExcessDegrade`). A request for write access to a tree the host only lets it read degrades to read access;
4. builds a **new interpreter** holding only the granted modules, with `rt.Gate` in front of it and registration frozen, so a plugin shares nothing with the host or with other plugins;
5. runs the rest of the file in it.

A plugin that has no `(requires …)` gets the bare language. A plugin cannot change its grant: its manifest is read once, before it runs, and what it asks for after that is nothing.

To show a person what a plugin wants before it runs, call `std.ReadManifest(path)` and `manifest.Plan(hostPolicy, base)`, which return the grant and the refused items without loading anything.

## The brote command

The command runs your own scripts with your own rights, so with no options it grants every module unscoped, as if the policy were `(policy (profile full))`. Options narrow that.

| Option | Meaning |
|---|---|
| `-sandbox` | start from `(profile safe)`: computation, the clock and randomness, and nothing that reaches the machine |
| `-policy FILE` | start from the policy in `FILE`. Directories in it mean the directory the file is in |
| `-allow SPEC` | grant something; repeatable. Naming one moves the start from everything to `safe`, so `-allow fs.read=data` means that and no more |
| `-deny MODULE` | remove a module from whatever the start was; repeatable |
| `-print-policy` | print the policy that would apply and exit |

A SPEC is a module name (`-allow regexp`) or `module.field=value,value`:

| SPEC | Grants |
|---|---|
| `fs.read=data,/etc/hosts` `fs.write=out` | directory trees |
| `net=api.example.com,*.github.com` | hosts. `net.methods=get,post` adds methods, `net.private=true` allows local addresses |
| `exec=git,/usr/bin/jq` | commands. `exec.env=PATH,HOME` gives them variables |
| `env=HOME,LANG` | variables scripts can read |

<!-- run-shell -->
```console
$ brote -print-policy -allow fs.read=data -allow net=api.example.com
(policy
  (path)
  (encoding)
  (regexp)
  (digest)
  (time (max-sleep "10s"))
  (rand)
  (fs (read "data") (max-file 16777216))
  (net (allow "api.example.com") (methods "get" "head") (private #f) (max-body 5242880) (timeout "30s")))
$ brote -deny exec -deny net -e '(sha256 "")'
"e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
```

When any of these options is given, `load` is confined to the `fs` grant, as `rt.Gate` does for an embedder. A script file named on the command line is read by the command itself, whatever the policy says: you chose it.

## What is tested

Each statement above has a test in `std/` or `cmd/brote/`:

| Statement | Test |
|---|---|
| An ungranted module has no procedures | `TestUngrantedIsAbsent`, `TestNothingGrantedNothingInstalled` |
| A policy prints and parses back equal; bad ones are errors | `TestPolicyRoundTrips`, `TestParseRejects`, `FuzzParsePolicy` |
| A grant is within the host and within the request | `TestIntersectNeverWidens`, `TestIntersectCases` |
| `..`, symbolic links and unlisted paths cannot leave a tree | `TestFSEscapesAreRefused`, `TestFSSymlinkOutOfAWriteTree` |
| Private addresses need their own grant, redirects are checked | `TestHTTPPrivateAddressesNeedTheirOwnGrant`, `TestHTTPRedirects`, `TestPrivateAddr` |
| A per-caller network check holds at every redirect hop and connection, and cannot widen | `TestANetCheckRefusesARedirectOutsideTheCallersGrant`, `TestANetCheckJudgesTheAddressANameResolvesToForEachCaller`, `TestANetCheckJudgesTheMethodOfEachHop`, `TestANetCheckCannotWidenTheInstalledPolicy`, `TestANetCheckIsAskedAtEveryStepWithTheCallersContext` |
| Arguments are never run by a shell; only granted programs start | `TestExecRunsGrantedCommandsWithoutAShell`, `TestExecRefusesWhatIsNotGranted` |
| `load` stays inside the grant | `TestPluginLoadStaysInsideTheGrant`, `TestPluginLoadCannotFollowASymlinkOut` |
| A plugin gets the intersection and nothing else | `TestPluginGetsOnlyWhatItAskedAndTheHostHas`, `TestPluginExcess` |
| The command's options | `cmd/brote/policy_test.go` |
| Every procedure is documented in [std.md](std.md) | `TestStdDocCoverage` |
