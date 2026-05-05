# gazelle_policy

`gazelle_policy` is a small Gazelle extension for BUILD-file policies that
should live beside the packages they govern.

The golden path is intentionally short:

```python
# gazelle:policy import("std:policies/required_tags.star")
# gazelle:policy import("std:policies/file_rollup.star")
# gazelle:policy use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:policy use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
```

Those two imports load stock policies from the bundled `std` mount. `use(...)`
activates them for a relative package scope and supplies their parameters.
Closer activations layer over farther ones, so a child package can override only
the parameter it cares about:

```python
# inherited `kinds` stays in force; only `tags` changes here
# gazelle:policy use("required_tags", scope = ".", tags = ["team:child"])
```

## Add the extension

```python
load("@gazelle//:def.bzl", "gazelle_binary")

gazelle_binary(
    name = "gazelle_policy",
    languages = ["@gazelle_policy//language/policy"],
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

Policy modules resolve through a small mount table:

```text
std:<path>    bundled gazelle_policy standard library
root:<path>   file path anchored at the repository root
<path>        relative to the importing .star file, or to the BUILD package for import(...)
```

Today the runtime exposes the `std` and `root` mounts. The mount table is the
internal abstraction, so other named mounts can be added later without changing
the module language.

## Reuse or customize

Stock policies are importable entrypoints:

```python
std:policies/required_tags.star
std:policies/file_rollup.star
```

If you want repo-specific names or defaults, load the bundled helper library
from your own `.star` entrypoint:

```python
load("std:lib/file_rollup.star", "file_rollup_policy")
load("std:lib/required_tags.star", "required_tags_policy")

required_tags_policy(
    name = "rust_required_tags",
    kinds = ["rust_library", "rust_binary", "rust_test"],
)

file_rollup_policy(
    name = "rust_files",
    local_name = "all_sources",
    recursive_name = "all_sources_recursive",
    include = ["*.rs", "BUILD.bazel"],
)
```

Then import the repo-owned entrypoint:

```python
# gazelle:policy import("root:build/policies/rust.star")
# gazelle:policy use("rust_required_tags", scope = "...", tags = ["team:runtime"])
# gazelle:policy use("rust_files", scope = "...")
```

Supported scopes are `"."`, `"..."`, `"bar"`, and `"bar/..."`; they are
relative to the package containing the directive.

To exempt exactly one following rule:

```python
# gazelle:policy exempt("required_tags", reason = "vendored target")
rust_library(
    name = "vendored",
)
```

## Starlark host API

The built-in library is ordinary Starlark layered over a deliberately small host:

```python
gazelle_policy.param(type, required = False, default = None)
gazelle_policy.rule_policy(name, params = {}, apply = fn)
gazelle_policy.package_policy(name, params = {}, apply = fn)
```

Rule callbacks receive `(ctx, rule)`. Package callbacks receive `(ctx)`.

```text
rule.kind
rule.name
rule.matches_kind(patterns)
rule.list_attr(name)
rule.ensure_list_attr_contains(name, values)

ctx.rel
ctx.policy_name
ctx.params
ctx.matching_files(include)
ctx.ensure_filegroup(name, srcs, public = False)
ctx.remove_filegroup(name)
ctx.child_exports(name)
ctx.export(name, label)
```

`params` is a real policy contract: unknown names, missing required params, and
wrong types are rejected instead of silently falling through.

## Current limits

- Directive comments are a tiny one-command language, not general Starlark.
- The built-in mount table exposes `std` and `root`; user-configured mounts are
  not surfaced yet.
- The host currently exposes safe string-list edits and filegroup generation,
  not arbitrary BUILD AST mutation.
- `required_tags` only rewrites literal string-list attrs; complex
  `select(...)`-style expressions are skipped rather than guessed at.
- Recursive rollups are deliberately conservative: if a selective Gazelle run
  has not covered every relevant child package, ancestor recursive outputs are
  left untouched instead of being rewritten from partial knowledge.

See [`docs/starlark-api-redesign.md`](docs/starlark-api-redesign.md) for the
design rationale, [`examples/`](examples/) for copyable policy files, and
[`tests/apply_mvp`](tests/apply_mvp/) for an end-to-end fixture.
