# gazelle_fold

`gazelle_fold` is a small Gazelle extension for folding over a BUILD tree from
leaf packages back toward the root. As it walks upward, it can build parent
targets from child exports, update local targets, and enforce project-local
BUILD policies beside the packages they govern.

That shape is useful in large monorepos, especially when humans and coding
agents are both writing BUILD files: teams can encode local conventions once and
keep generated edits aligned with the repo's own build patterns.

The basic setup is short:

```python
# gazelle:fold import("std:folds/filegroup_rollup.star")
# gazelle:fold import("std:rewrites/required_tags.star")
# gazelle:fold import("std:policies/forbidden_deps.star")
# gazelle:fold use("filegroup_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
# gazelle:fold use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:fold use("forbidden_deps", scope = "app/...", kinds = ["rust_library"], deny = ["//legacy/..."])
```

Those imports load stock definitions from the bundled `std` mount. `use(...)`
activates them for a relative package scope and supplies their parameters.
Nearer activations layer over farther ones, so a child package can override only
the parameter it cares about:

```python
# inherited `kinds` stays in force; only `tags` changes here
# gazelle:fold use("required_tags", scope = ".", tags = ["team:child"])
```

The bundled definitions show the three things the extension can do:

```text
filegroup_rollup      fold child exports into ancestor targets
required_tags    modify local targets in place
forbidden_deps   reject local policy violations
```

`filegroup_rollup` is the canonical fold example: leaf packages export local
source groups, parents combine child exports with their own local files, and a
full walk carries the recursive rollup all the way to the root.

For a tree like:

```text
app/
├── BUILD.bazel
├── root.rs
└── child/
    ├── BUILD.bazel
    ├── lib.rs
    └── grandchild/
        ├── BUILD.bazel
        └── lib.rs
```

With one directive at the root:

```python
# gazelle:fold import("std:folds/filegroup_rollup.star")
# gazelle:fold use("filegroup_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
```

the leaf package exports its local files:

```python
# child/grandchild/BUILD.bazel
filegroup(
    name = "all_sources",
    srcs = [
        "BUILD.bazel",
        "lib.rs",
    ],
)

filegroup(
    name = "all_sources_recursive",
    srcs = [":all_sources"],
    visibility = ["//visibility:public"],
)
```

its parent adds the child export to its own local files:

```python
# child/BUILD.bazel
filegroup(
    name = "all_sources_recursive",
    srcs = [
        ":all_sources",
        "//child/grandchild:all_sources_recursive",
    ],
    visibility = ["//visibility:public"],
)
```

and the root receives the whole subtree:

```python
# BUILD.bazel
filegroup(
    name = "all_sources_recursive",
    srcs = [
        ":all_sources",
        "//child:all_sources_recursive",
    ],
    visibility = ["//visibility:public"],
)
```

Each package contributes only its local files and the exports from its direct
children. The recursive target emerges from the fold instead of one giant
repo-wide declaration.

## Install

After `gazelle_fold` is published to the Bazel Central Registry, add it beside
Gazelle in your root module:

```python
bazel_dep(name = "gazelle", version = "0.50.0")
bazel_dep(name = "gazelle_fold", version = "0.1.0")
```

## Add the extension

```python
load("@gazelle//:def.bzl", "gazelle_binary")

gazelle_binary(
    name = "gazelle_fold",
    languages = ["@gazelle_fold//language/fold"],
    version = 2,
)
```

Run normal Gazelle apply/check flows:

```bash
bazelisk run //:gazelle
bazelisk run //:gazelle_ci
```

The repo pins Bazel through `.bazelversion`, so use `bazelisk` for local builds
and tests as well.

For a tiny repo that consumes `gazelle_fold` as a dependency instead of as the
root module, see [`examples/bzlmod`](examples/bzlmod). CI runs that module
separately so the public integration path stays honest.

For a package-local rule synthesis example, see
[`examples/rust_clippy`](examples/rust_clippy). It keeps one managed Clippy
target per Rust package from a single root activation.

## Module paths

Modules resolve through a small mount table:

```text
std:<path>    bundled gazelle_fold standard library
root:<path>   file path anchored at the repository root
<path>        relative to the importing .star file, or to the BUILD package for import(...)
```

Today the runtime exposes the `std` and `root` mounts. The mount table is the
internal abstraction, so other named mounts can be added later without changing
the module language.

## Reuse or customize

Stock definitions are importable entrypoints for common fold steps and rule hooks:

```python
std:folds/filegroup_rollup.star
std:rewrites/required_tags.star
std:policies/forbidden_deps.star
```

For custom behavior, write an ordinary repo-owned `.star` module against the
host API. The bundled library intentionally stays small; use the stock
definitions when they fit, and drop to custom Starlark only when you need a
different behavior.

Supported scopes are `"."`, `"..."`, `"bar"`, and `"bar/..."`; they are
relative to the package containing the directive.

To skip one rule action for exactly one following target:

```python
# gazelle:fold skip("required_tags", reason = "vendored target")
rust_library(
    name = "vendored",
)
```

## Starlark host API

The bundled modules are ordinary Starlark layered over a deliberately small host:

```python
gazelle_fold.param(type, required = False, default = None)
gazelle_fold.fold(name, params = {}, apply = fn)
gazelle_fold.rewrite(name, params = {}, apply = fn)
gazelle_fold.policy(name, params = {}, apply = fn)
gazelle_fold.rule(kind, name, present = True, attrs = {})
gazelle_fold.export(name, label)
```

Fold callbacks receive `(ctx)`. Rewrites and policies receive `(ctx, rule)`.
Folds can read child exports, emit local targets, and pass state upward to
ancestors. Rewrites change local rules. Policies report violations and can fail
the run.

```text
rule.name
rule.matches_kind(patterns)
rule.ensure_list_attr_contains(name, values)
rule.deps_matching(patterns)

ctx.params
ctx.matching_files(include)
ctx.rules_matching(kinds)
ctx.child_exports(name)
ctx.report_violation(message)  # policies only
```

Fold callbacks return package outputs such as `gazelle_fold.rule(...)` and
`gazelle_fold.export(...)`. `rule(...)` covers every emitted target kind,
including native kinds such as `filegroup`, so folds describe one target shape
instead of choosing between target-specific constructors. `attrs` accepts literal
bools, strings, and lists or tuples of strings. Managed rules declare whether
they should be present, so the host owns the ensure/remove machinery and fold
authors describe the final BUILD shape instead of scripting mutations. Omitting a
managed output is a no-op; explicit absence keeps deletion ownership local and
prevents a fold from silently claiming unrelated package rules.

`params` is a real definition contract: unknown names, missing required params,
and wrong types are rejected instead of silently falling through.

## Current limits

- Directive comments use a tiny one-command language, not general Starlark.
- The built-in mount table exposes `std` and `root`; user-configured mounts are
  not surfaced yet.
- The host exposes safe string-list edits plus declarative package outputs, not
  arbitrary BUILD AST mutation.
- `required_tags` only rewrites literal string-list attributes; complex
  `select(...)`-style expressions are skipped rather than guessed at.
- `forbidden_deps` reports direct labels from literal `deps` lists and fails the
  Gazelle run before files are written. Patterns are absolute Bazel labels such
  as `//legacy:old` or subtree selectors such as `//legacy/...`; non-literal
  `deps` expressions fail closed because they cannot be validated safely.
- Recursive rollups are deliberately conservative: if a selective Gazelle run
  has not covered every relevant child package, ancestor recursive outputs are
  left untouched instead of being rewritten from partial knowledge.

See [`docs/starlark-api-redesign.md`](docs/starlark-api-redesign.md) for the
fold design rationale, [`examples/`](examples/) for copyable examples, and
[`tests/apply_mvp`](tests/apply_mvp/) for an end-to-end fixture.
