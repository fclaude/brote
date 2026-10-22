# Provenance

Brote's interpreter was written as part of Nodwyn, by Francisco
Claude-Faust, and extracted here on 2026-10-22.

- **Source:** `internal/scheme/` of Nodwyn (a coding agent whose repository is
  not public yet) at commit `3846cd7f0deb73265c1c59e65d98d7653187ddd4`.
  Thirty-eight commits touched that directory; all are by the author above. The tree was
  copied without its history, which stays in Nodwyn's.
- **`scheme/`** is that directory, file for file. Two differences from the
  source: the package comment in `value.go`, and one line in `scheme_test.go`
  (`TestDocCoverage` reads `../docs/scheme.md`, one directory up from where it
  was). `embed_test.go` and `example_test.go` are new.
- **`docs/scheme.md`** is the language sections (§§1–7 of Nodwyn's
  `docs/scheme.md`, plus §8 and §10 as §8 and §9) with the parts about Nodwyn's
  guard, agent loop, host primitives and integrations removed or reworded for a
  host-neutral reader. Section numbers §1–§7 are the same as in Nodwyn's, so
  the references in code comments and in the `register-tool` doc string stay
  valid.
- **`repl/`** is `plain.SchemeREPL` from Nodwyn's `internal/frontend/plain`,
  with its prompts as options and a per-form context hook.
- **`cmd/brote/`** replaces Nodwyn's `repl` and `eval` commands as the
  standalone interpreter.
- **Comments and messages.** Comments that cite "ADR n", "SPEC", "L2" or "Pn"
  refer to Nodwyn's `docs/adr` and `SPEC.md` at the extraction commit; they are
  the design record for the behaviour. Two error messages and one doc string
  keep their Nodwyn wording because Nodwyn's tests and prompts depend on the
  text.
- **Licence.** MIT, as Coigüe, held by the same author; Nodwyn, where the code
  was written, has no licence file.
