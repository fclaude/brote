# The extended library

Brote's language reference covers the pure language. This page covers `std`, the batteries on top of it: files, the network, programs, the environment, the clock, randomness, regular expressions, encodings, digests and paths. They come from Go's standard library, and a program only has the ones its host granted. Which ones those are is the subject of [capabilities](capabilities.md); this page is about what each one does.

The `brote` command grants all of them unless you ask it not to. A Go program that embeds Brote gets none until it installs them with `std.Install`. From inside a script, `(modules)` lists what you have, and `(require 'fs)` fails with a clear message when you were not given `fs`.

| Module | What it is for | Needs a scope |
|---|---|---|
| [`path`](#path) | file names as text | no |
| [`encoding`](#encoding) | Base64, hex, URL escaping, CSV | no |
| [`regexp`](#regexp) | regular expressions (RE2) | no |
| [`digest`](#digest) | SHA-256, SHA-1, MD5, HMAC | no |
| [`time`](#time) | the clock, dates, sleeping | a sleep limit |
| [`rand`](#rand) | random numbers and tokens | no |
| [`env`](#env) | environment variables | the variables |
| [`fs`](#fs) | files and directories | the directories |
| [`net`](#net) | HTTP | the hosts |
| [`exec`](#exec) | running programs | the commands |

A module that was not granted has no procedures at all, so using one is an `unbound variable` error. The examples below that print something are run by the test suite, in a `brote` that was given everything.

## Introspection

| Procedure | What it gives back |
|---|---|
| `(modules)` | the granted modules, as a list of symbols |
| `(require 'name …)` | `#t` when every named module was granted. Otherwise an error that names the module and lists what you do have |
| `(module-info 'name)` | what the module may reach, as data in the policy's own syntax, or `#f` when it was not granted |

<!-- run -->
```lisp
(modules)
(require 'regexp 'digest)
(module-info 'regexp)
(guard (e (#t (assoc-ref e 'message))) (require 'quantum))
```

```text
(path encoding regexp digest time rand env fs net exec)
#t
()
"require: unknown module \"quantum\" (modules: path, encoding, regexp, digest, time, rand, env, fs, net, exec)"
```

A plugin that can do without a module checks `(module-info 'net)` and degrades. One that cannot calls `(require 'net)` first and fails with a message a person can act on.

## path

These work on text and never touch the disk. They use the separator of the platform the program runs on.

| Procedure | What it gives back |
|---|---|
| `(path-join element …)` | the elements joined and cleaned. Empty elements are skipped |
| `(path-base path)` | the last element: `"a/b.txt"` gives `"b.txt"` |
| `(path-dir path)` | everything before the last element: `"a/b.txt"` gives `"a"` |
| `(path-ext path)` | the extension with its dot, or `""` |
| `(path-clean path)` | the path with `.`, `..` and doubled separators resolved |

<!-- run -->
```lisp
(path-join "notes" "2024" "../today.txt")
(path-base "notes/today.txt")
(path-dir "notes/today.txt")
(path-ext "archive.tar.gz")
(path-clean "a//b/./c/..")
```

```text
"notes/today.txt"
"today.txt"
"notes"
".gz"
"a/b"
```

## encoding

Strings are text, so what is encoded is the UTF-8 bytes of a string, and what is decoded must be text again: a Base64 or hex string that decodes to bytes that are not UTF-8 is an error.

| Procedure | What it gives back |
|---|---|
| `(base64-encode text [url-safe])` `(base64-decode text [url-safe])` | Base64. With `#t`, the URL alphabet (`-` and `_`) without padding |
| `(hex-encode text)` `(hex-decode text)` | lower-case hexadecimal |
| `(url-encode text)` `(url-decode text)` | escaping for a URL query value: a space is `+`, reserved characters are `%XX` |
| `(csv-parse text [delimiter])` | a list of rows, each a list of strings. Quotes, doubled quotes and newlines inside quotes are handled |
| `(csv-emit rows [delimiter])` | CSV text for a list of rows whose cells are strings or numbers |
| `(ini-parse text)` | a list of `(section entries…)` pairs in order: `[section]` headers, `key=value` lines, `;`/`#` comments, a valueless key with value `""`, keys before any header in the `""` section |

<!-- run -->
```lisp
(base64-encode "héllo")
(base64-decode "aMOpbGxv")
(url-encode "a b&c=d")
(csv-parse "name,score\n\"Ruiz, Ana\",9\n")
(csv-emit '(("name" "score") ("Ruiz, Ana" 9)))
```

```text
"aMOpbGxv"
"héllo"
"a+b%26c%3Dd"
(("name" "score") ("Ruiz, Ana" "9"))
"name,score\n\"Ruiz, Ana\",9\n"
```

`csv-parse` allows rows of different lengths and a stray quote in an unquoted field, because the files people have are like that. It reads at most a million cells.

## regexp

Patterns are Go's [RE2 syntax](https://pkg.go.dev/regexp/syntax). RE2 has no backreferences, and in exchange a match takes time proportional to the length of the text whatever the pattern is, so a pattern from someone else cannot hang the program. A pattern may be 4096 bytes long.

| Procedure | What it gives back |
|---|---|
| `(regexp-matches? pattern text)` | `#t` when the pattern matches anywhere in the text. Anchor with `^` and `$` to match all of it |
| `(regexp-match pattern text)` | the first match as a list: the whole match, then each group (`#f` for a group that did not take part), or `#f` |
| `(regexp-match-all pattern text)` | every match, each as `regexp-match` gives it (at most 100,000) |
| `(regexp-replace pattern text replacement)` | the text with every match replaced. In the replacement `$1` and `${name}` are groups and `$$` is a dollar sign |
| `(regexp-split pattern text)` | the pieces between matches |
| `(regexp-quote text)` | the text with its metacharacters escaped, to match it literally |

<!-- run -->
```lisp
(regexp-match "(\\d+)-(\\d+)?" "call 555- now")
(regexp-match-all "[a-z]+(\\d)" "ab1 cd2 e")
(regexp-replace "(\\w+)@(\\w+)" "me@host you@there" "$2:$1")
(regexp-split ",\\s*" "a, b,c")
(regexp-matches? (regexp-quote "1+1=2") "so 1+1=2!")
```

```text
("555-" "555" #f)
(("ab1" "1") ("cd2" "2"))
"host:me there:you"
("a" "b" "c")
#t
```

## digest

Each takes a string and gives lower-case hex of the digest of its UTF-8 bytes.

| Procedure | What it gives back |
|---|---|
| `(sha256 text)` `(sha512 text)` | the SHA-2 digests |
| `(sha1 text)` `(md5 text)` | for checksums and old formats. They are broken as security measures |
| `(hmac-sha256 key message)` | the HMAC-SHA-256 of the message under the key |
| `(ed25519-sign private-key message)` | the Ed25519 signature, hex. The key is 64 bytes (a full private key) or 32 bytes (a seed), hex |
| `(ed25519-verify public-key message signature)` | `#t` or `#f`. The public key (32 bytes, hex) must be one the policy granted with `(digest (keys …))`; a key it does not name is an error |
| `(ecdsa-sign private-key message)` `(ecdsa-verify public-key message signature)` | the same for ECDSA P-256: the private key is a 32-byte scalar, the public key a compressed point, the signature DER, all hex |

Signing needs no grant: the private key is the caller's own credential, and a gate sees it as one. Verifying is authorization, so the policy names the public keys a script may verify with, `(digest (keys "hex" …))` or `(keys *)`; a signature that does not verify is `#f`, a key the policy does not grant is an error.

<!-- run -->
```lisp
(sha256 "abc")
(hmac-sha256 "key" "The quick brown fox jumps over the lazy dog")
```

```text
"ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
"f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"
```

## time

Times are seconds since 1970-01-01 UTC, as numbers.

| Procedure | What it gives back |
|---|---|
| `(time-now)` | the current time, a float with the fraction |
| `(time-ms)` | the current time in whole milliseconds |
| `(time-format seconds layout [zone])` | the time as text. The layout is `"rfc3339"`, `"date"`, `"time"`, `"datetime"` or a [Go layout](https://pkg.go.dev/time#pkg-constants) such as `"Mon Jan 2 15:04"`. The zone is `"utc"` (the default) or `"local"` |
| `(time-parse text layout [zone])` | seconds, or an error if the text does not fit the layout. A zone in the text wins |
| `(time-sleep seconds)` | pause. A host-set limit (10 seconds unless the policy says otherwise) applies to each call, and cancelling the evaluation ends a sleep early |

<!-- run -->
```lisp
(time-format 0 "rfc3339")
(time-format 86400 "date")
(= (time-parse "2024-02-29" "date") 1709164800)
```

```text
"1970-01-01T00:00:00Z"
"1970-01-02"
#t
```

## rand

| Procedure | What it gives back |
|---|---|
| `(rand-int n)` | an integer from 0 up to but not including `n` |
| `(rand-float)` | a float from 0 up to but not including 1 |
| `(rand-shuffle list)` | a new list in random order |
| `(rand-hex n)` | `2n` hexadecimal digits from the system's cryptographic source, for tokens and identifiers (`n` is at most 65,536) |

`rand-int`, `rand-float` and `rand-shuffle` are for games and sampling: they cannot be seeded and are not for secrets. Use `rand-hex` for those.

## env

The host names the variables a script may see, or says `*` for all of them.

| Procedure | What it gives back |
|---|---|
| `(env-get name [default])` | the value, or the default (`#f` if none) when the variable is not set. A variable the host did not grant is an error, not "unset" |
| `(env-list)` | the granted variables that are set, as an association list |

What these return is tainted `external:env`, for hosts that follow provenance.

## fs

Files and directories, inside the trees the host granted. A path is taken against the directory the host chose (the current directory for the `brote` command). A path that leaves a granted tree, by `..` or by a symbolic link, is an error, and so is a path in no granted tree. A tree granted for writing can also be read.

| Procedure | What it gives back |
|---|---|
| `(fs-read-text path)` | the file as a string. Bytes that are not UTF-8 become U+FFFD. A file over the host's size limit (16 MB unless the policy says otherwise) is an error |
| `(fs-write-text path text)` | write the file, creating it or replacing what it held |
| `(fs-append-text path text)` | add to the end of the file, creating it if needed |
| `(fs-list path)` | the names in a directory, sorted |
| `(fs-stat path)` | an association list: `"size"`, `"dir"`, `"mode"` (such as `"0644"`) and `"mtime"` |
| `(fs-exists? path)` | `#t` or `#f`. A path outside the grant is an error, not `#f` |
| `(fs-mkdir path)` | make the directory and any missing parents. One that exists is fine |
| `(fs-remove path)` | delete a file or an empty directory. There is no recursive delete |
| `(fs-rename from to)` | move within one granted tree, replacing a file that is there |
| `(fs-watch path [interval])` | a watch over a granted directory, polled every `interval` seconds (default 1) |
| `(fs-watch-changes watch)` | the names that appeared, changed or were removed since the last call, sorted; drains the queue |
| `(fs-watch-stop! watch)` | stop the watch; void |
| `(watch? x)` | |

A script writes, reads back, lists and cleans up:

<!-- run-shell -->
```console
$ brote -e '(begin (fs-mkdir "notes") (fs-write-text "notes/a.txt" "one") (fs-append-text "notes/a.txt" " two") (fs-write-text "notes/b.txt" "") (list (fs-read-text "notes/a.txt") (fs-list "notes") (assoc-ref (fs-stat "notes/a.txt") "size")))'
("one two" ("a.txt" "b.txt") 7)
```

With the file system limited to one directory, a path outside it is refused before anything is read:

<!-- run-shell -->
```console
$ brote -allow fs.read=. -e '(fs-read-text "/etc/hosts")'
brote: fs-read-text: /etc/hosts is outside the directories this host lets scripts read
$ brote -allow fs.read=. -e '(fs-write-text "x.txt" "no")'
brote: fs-write-text: x.txt is outside the directories this host lets scripts write
```

A host that wants to hear about every write, request and program, whether it succeeded or was refused, passes `std.WithAudit` to `Install`.

## net

One procedure sends an HTTP or HTTPS request, and a few Scheme definitions wrap it.

| Procedure | What it gives back |
|---|---|
| `(http-request method url [headers [body]])` | the response as an association list: `"status"`, `"headers"` (an alist with lower-case names), `"body"` (a string) and `"url"` (the final one after redirects) |
| `(http-get url [headers])` | `(http-request "GET" …)` |
| `(http-post url body [headers])` | `(http-request "POST" …)` |
| `(http-get-json url [headers])` | the response body parsed as JSON. A status of 400 or more is an error |
| `(http-post-json url value [headers])` | send `value` as JSON, return the parsed JSON reply. A status of 400 or more is an error |

A net grant also lets a script serve HTTP, not only call it:

| Procedure | What it gives back |
|---|---|
| `(serve addr routes)` | start a server on `addr` (`"host:port"`; port 0 picks a free one) serving `routes`, an alist of path to handler procedure; returns a server |
| `(server-address srv)` | the bound `"host:port"` |
| `(server-stop! srv)` | stop it; void |
| `(server? x)` | |

The handler is called with an alist `((method . "GET") (path . "/") (body . "…"))` and returns a string (sent with status 200) or a list `(status body)` with a status from 100 to 999. A path with no route gets a 404. Request bodies and responses are capped at `max-body`, and a handler runs under the `timeout`. Each handler call runs under the context of the evaluation that called `serve`, so a gate and a per-caller policy see that caller. Binding a loopback address is allowed whenever `net` is granted; binding any other host requires the host to be in the `(allow …)` list. A server runs until `server-stop!` or the host closes the runtime, not only until the evaluation that started it ends.

Headers are an association list of names and values: `'(("authorization" . "Bearer …"))`. A status of 400 or more from `http-request` is a result you look at, not an error. These are errors: a host the policy did not name, a method it did not allow (get and head unless it says more), a redirect to such a host, a body over the limit (5 MB), a timeout (30 seconds), and a connection to a private or local address the policy did not allow.

```lisp
(define repo (http-get-json "https://api.github.com/repos/golang/go"))
(assoc-ref repo "stargazers_count")

(http-post-json "https://api.example.com/items" '(("name" . "lamp")))
```

Redirects are followed, up to five, and each one is checked against the same list. A header you set for one host is not sent to another that a redirect leads to. Environment proxy settings are not used. A URL with a user name or password is refused; send credentials in a header.

The result of every procedure here carries the taint `external:http`.

## exec

| Procedure | What it gives back |
|---|---|
| `(proc-run argv [options])` | an association list: `"exit-code"`, `"stdout"` and `"stderr"` |
| `(proc-output argv [options])` | the standard output, or an error that carries the exit status and standard error when the exit status is not 0 |

`argv` is a list of strings: the command, then its arguments. It is started directly, **never through a shell**, so a space, `;`, `|`, `$(…)` or `*` in an argument is just text. The options are an association list: `"stdin"` (a string to feed it) and `"timeout"` (seconds, within the host's limit of 30 by default). The program runs in the directory the host chose, and sees only the environment variables the host granted. It gets at most 1 MB of output.

<!-- run-shell -->
```console
$ brote -e '(proc-output (list "echo" "a; touch gone $(id) | cat"))'
"a; touch gone $(id) | cat\n"
$ brote -e '(assoc-ref (proc-run (list "cat") (list (cons "stdin" "fed"))) "stdout")'
"fed"
$ brote -allow exec=echo -e '(proc-run (list "ls"))'
brote: proc-run: ls is not a command this host lets scripts run
```

A command the host did not grant is an error, and so is one that cannot start, one that runs past its timeout (it is stopped, with the processes it started), and one that writes more than the limit. A nonzero exit is a result.

What a granted command does is the command's business. Allowing `git` allows what `git` can be told to do, and allowing `sh`, `python` or `env` allows everything. Grant programs the way you would grant them to a person you do not fully trust.

## One interpreter, different grants

`std.Install` takes one `Policy` per interpreter and enforces it inside the procedures. A host whose modules each declare their own grant installs the widest policy any of them may have, and narrows it per call with the caller's own policy: the network with `std.WithNetCheck`, which is asked at every step of a request, and the rest in its `scheme.Gate`.

| Function or method | Answers |
|---|---|
| `std.WithNetCheck(fn)` | an `Install` option: `fn(ctx, ev)` is asked, after the installed policy, before the request is sent (`std.NetRequest`), before each redirect hop with the method it uses (`std.NetRedirect`), immediately before each socket connects, with the address it connects to (`std.NetConnect`), and before `serve` binds a server (`std.NetListen`). `ctx` carries the evaluation's values. An error refuses the step |
| `(*Policy).CheckNetEvent(ev)` | all a `WithNetCheck` function needs: `CheckNet` for a request or a hop, `CheckAddr` for a connection, `CheckListen` for a bind |
| `(*Policy).CheckNet(method, rawURL)` | whether a script holding this policy may send that request: `net` granted, the scheme, the host and port against the allow list, the method, and (for a host written as an IP address) the private-address rule |
| `(*Policy).CheckAddr(host, ip)` | whether it may connect to `ip`, the address `host` resolved to: a loopback, private, link-local, multicast, unspecified or reserved one needs `(private #t)` |
| `(*Policy).CheckExec(name)` | whether it may start that command (the first element of `proc-run`'s list) |
| `(*Runtime).ModuleOf(name)` | which module installed a procedure (`"net"` for `http-request`); `""` for `require`, `modules` and `module-info` |

Each check returns `nil` or an error written to be shown to a person, such as `net: evil.com:443 is not a host this module may reach (allowed hosts: api.example.com)`. They decide what the installed procedures decide, so a check that is looser than the installed policy cannot reach further than that policy: the installed policy stays the outer limit. A Gate sees only `http-request`'s arguments, not where a redirect leads or what address a name resolves to, so the network check belongs in `WithNetCheck`. There is no `CheckFS`, because a check on the text of a path cannot see a symbolic link, and the real containment is the operating system's (`os.Root`); give modules with different file grants different interpreters. The whole pattern is in [embedding](embedding.md#different-policies-for-different-callers).
