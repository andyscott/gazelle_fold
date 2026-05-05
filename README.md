# gazelle_fold

`gazelle_fold` is a small Gazelle extension for folding over a BUILD tree from
leaf packages back toward the root. As the fold climbs, it can synthesize
ancestor-facing targets from child exports, modify local targets, and enforce
project-local BUILD policies beside the packages they govern.

That shape is useful in large monorepos, especially when humans and coding
agents are both writing BUILD files: teams can encode local conventions once and
keep automated edits converging toward the repo's own build patterns.

The golden path is intentionally short:

```python
# gazelle:fold import("std:folds/file_rollup.star")
# gazelle:fold import("std:rewrites/required_tags.star")
# gazelle:fold import("std:policies/forbidden_deps.star")
# gazelle:fold use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
# gazelle:fold use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:fold use("forbidden_deps", scope = "app/...", kinds = ["rust_library"], deny = ["//legacy/..."])
```

Those imports load stock definitions from the bundled `std` mount. `use(...)`
activates them for a relative package scope and supplies their parameters.
Closer activations layer over farther ones, so a child package can override only
the parameter it cares about:

```python
# inherited `kinds` stays in force; only `tags` changes here
# gazelle:fold use("required_tags", scope = ".", tags = ["team:child"])
```

The bundled definitions show the three parts of the model:

```text
file_rollup      fold child exports into ancestor targets
required_tags    modify local targets in place
forbidden_deps   reject local policy violations
```

`file_rollup` is the canonical fold-shaped example: leaf packages export local
source groups, parents combine child exports with their own local files, and a
full walk can carry the recursive rollup all the way to the root.

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

one root directive:

```python
# gazelle:fold import("std:folds/file_rollup.star")
# gazelle:fold use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
```

lets the leaf export itself:

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

the parent fold that export upward:

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

and the root receive the whole subtree:

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

Each package contributes only its local files plus the exports from its direct
children; the recursive target emerges from the fold rather than from one giant
repo-global declaration.

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
std:folds/file_rollup.star
std:rewrites/required_tags.star
std:policies/forbidden_deps.star
```

If you want repo-specific names or defaults, load the bundled helper library
from your own `.star` entrypoint:

```python
load("std:lib/file_rollup.star", "file_rollup_fold")
load("std:lib/forbidden_deps.star", "forbidden_deps_policy")
load("std:lib/required_tags.star", "required_tags_rewrite")

required_tags_rewrite(
    name = "rust_required_tags",
    kinds = ["rust_library", "rust_binary", "rust_test"],
)

file_rollup_fold(
    name = "rust_files",
    local_name = "all_sources",
    recursive_name = "all_sources_recursive",
    include = ["*.rs", "BUILD.bazel"],
)

forbidden_deps_policy(
    name = "rust_forbidden_deps",
    kinds = ["rust_library", "rust_binary", "rust_test"],
    deny = ["//legacy/..."],
)
```

Then import the repo-owned entrypoint:

```python
# gazelle:fold import("root:build/gazelle_fold/rust.star")
# gazelle:fold use("rust_required_tags", scope = "...", tags = ["team:runtime"])
# gazelle:fold use("rust_files", scope = "...")
```

Supported scopes are `"."`, `"..."`, `"bar"`, and `"bar/..."`; they are
relative to the package containing the directive.

To skip exactly one following rule action:

```python
# gazelle:fold skip("required_tags", reason = "vendored target")
rust_library(
    name = "vendored",
)
```

## Starlark host API

The built-in library is ordinary Starlark layered over a deliberately small host:

```python
gazelle_fold.param(type, required = False, default = None)
gazelle_fold.fold(name, params = {}, apply = fn)
gazelle_fold.rewrite(name, params = {}, apply = fn)
gazelle_fold.policy(name, params = {}, apply = fn)
```

Fold callbacks receive `(ctx)`. Rewrites and policies receive `(ctx, rule)`.
Folds can read child exports, emit local targets, and export state upward for
ancestors. Rewrites change local rules. Policies are the judging surface: they
can report violations and fail the run.

```text
rule.kind
rule.name
rule.matches_kind(patterns)
rule.list_attr(name)
rule.ensure_list_attr_contains(name, values)
rule.deps_matching(patterns)

ctx.rel
ctx.name
ctx.params
ctx.matching_files(include)
ctx.ensure_filegroup(name, srcs, public = False)
ctx.remove_filegroup(name)
ctx.child_exports(name)
ctx.export(name, label)
ctx.report_violation(message)  # policies only
```

`params` is a real definition contract: unknown names, missing required params, and
wrong types are rejected instead of silently falling through.

## Current limits

- Directive comments are a tiny one-command language, not general Starlark.
- The built-in mount table exposes `std` and `root`; user-configured mounts are
  not surfaced yet.
- The host currently exposes safe string-list edits and filegroup generation,
  not arbitrary BUILD AST mutation.
- `required_tags` only rewrites literal string-list attrs; complex
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
