# Starlark API design

## Recommendation

The public surface should have four layers:

```text
BUILD directives       activate named policies by package scope
std:policies/*         ready-made policies users can import directly
std:lib/*              helper factories for repo-owned policy modules
gazelle_policy host    a tiny safe runtime for custom policies
```

Module paths resolve through mounts rather than Bazel labels:

```text
std:<path>    bundled gazelle_policy standard library
root:<path>   repository-rooted file
<path>        relative to the importer
```

Today only `std` and `root` are exposed. Internally, both are entries in the
same mount table, which leaves room for future configured mounts without
changing the path language.

## Why this shape

The first constructor-style MVP used Starlark syntax without really using the
language. Aspect Gazelle's Orion model showed the better seam: real modules,
`load()`, one predeclared host object, and typed callback contexts.

We keep that seam, but make the product much smaller:

- stock policies are importable directly
- helper modules are explicit and always loadable
- user-authored modules are normal mounted files
- the Go host owns the unsafe BUILD/Gazelle machinery

## Golden path

```python
# gazelle:policy import("std:policies/required_tags.star")
# gazelle:policy import("std:policies/file_rollup.star")
# gazelle:policy use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:policy use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
```

An activation nearer the target package layers over farther activations, so a
child can override one field without repeating the entire contract:

```python
# gazelle:policy use("required_tags", scope = ".", tags = ["team:child"])
```

## Bundled modules

### Importable stock policies

```text
std:policies/required_tags.star
std:policies/file_rollup.star
std:policies/forbidden_deps.star
```

These register generic policy names driven entirely by `use(...)` params.

### Loadable helper library

```python
load("std:lib/required_tags.star", "required_tags_policy")
load("std:lib/file_rollup.star", "file_rollup_policy")
load("std:lib/forbidden_deps.star", "forbidden_deps_policy")
```

These let a repo define opinionated names and defaults without vendoring helper
code:

```python
required_tags_policy(
    name = "rust_required_tags",
    kinds = ["rust_library", "rust_binary", "rust_test"],
)

file_rollup_policy(
    name = "rust_files",
    include = ["*.rs", "BUILD.bazel"],
    local_name = "all_sources",
    recursive_name = "all_sources_recursive",
)

forbidden_deps_policy(
    name = "rust_forbidden_deps",
    kinds = ["rust_library", "rust_binary", "rust_test"],
    deny = ["//legacy/..."],
)
```

Repo-owned entrypoints can be imported from the `root` mount:

```python
# gazelle:policy import("root:build/policies/rust.star")
```

Inside a `.star` file, plain paths stay relative to the importing module:

```python
load("helpers.star", "helper")
load("../shared/common.star", "common")
```

## Minimal host API

```python
gazelle_policy.param(type, required = False, default = None)
gazelle_policy.rule_policy(name, params = {}, apply = fn)
gazelle_policy.package_policy(name, params = {}, apply = fn)
```

`params` is a schema, not a loose bag. The host rejects:

- unknown activation params
- missing required params
- wrong activation param types

Supported param types today:

```text
string
strings
bool
int
```

### Rule-policy callbacks

```python
def apply(ctx, rule):
    ...
```

```text
ctx.rel
ctx.policy_name
ctx.params

rule.kind
rule.name
rule.matches_kind(patterns)
rule.list_attr(name)
rule.ensure_list_attr_contains(name, values)
rule.remove_deps_matching(patterns)
```

Matching is intentionally an activation-time concern, not a registration-time
constraint. That is what lets stock policies be imported directly and configured
through `use(...)`.

### Package-policy callbacks

```python
def apply(ctx):
    ...
```

```text
ctx.rel
ctx.policy_name
ctx.params
ctx.matching_files(include)
ctx.ensure_filegroup(name, srcs, public = False)
ctx.remove_filegroup(name)
ctx.child_exports(name)
ctx.export(name, label)
```

`ensure_filegroup(...)` and `remove_filegroup(...)` are separate on purpose:
empty `srcs` is a legitimate Bazel value and should not secretly mean delete.

`ctx.child_exports(name)` returns:

```text
children.labels
children.complete
```

That preserves the current partial-walk safety rule: if Gazelle has not visited
every relevant child package, the host can keep ancestor recursive outputs
untouched instead of rebuilding them from partial knowledge.

## What lives where

### Go host

- directive parsing and scope inheritance
- mount resolution
- param validation and activation layering
- anchored exemptions
- package-tree traversal and partial-walk completeness
- safe BUILD AST mutations
- diagnostics and provenance

### Starlark library

- stock policy entrypoints
- reusable policy families
- parameter defaults and small compositions
- later helpers such as `mirror_attr_policy(...)`

## The current helpers

### `required_tags_policy(...)`

```python
def required_tags_policy(name, kinds = None, tags = []):
    def _apply(ctx, rule):
        active_kinds = ctx.params["kinds"] if kinds == None else kinds
        if not rule.matches_kind(active_kinds):
            return
        rule.ensure_list_attr_contains(
            name = "tags",
            values = ctx.params.get("tags", tags),
        )
```

When `kinds` is omitted, the helper declares it as a required activation param.
When `kinds` is supplied, it becomes an opinionated helper with fewer required
call-site arguments.

### `file_rollup_policy(...)`

```python
def file_rollup_policy(name, include = None, local_name = None, recursive_name = None):
    ...
```

The stock policy leaves all three fields to `use(...)`. Repo-owned helper calls
can bake them in as defaults.

### `forbidden_deps_policy(...)`

```python
def forbidden_deps_policy(name, kinds = None, deny = None):
    ...
```

This removes direct dependency labels from literal `deps` lists. `deny` accepts
absolute label patterns such as `//legacy:old` and package-subtree patterns such
as `//legacy/...`. The host normalizes relative deps before matching so `:old`
inside package `legacy` is covered by `//legacy/...`.

## Why `mirror_attr` should still be separate

File aggregation reasons about files and child exports. Attribute mirroring
reasons about peer rules in one package. Those are different problems even when
both mention `srcs`.

A future helper can likely be:

```python
mirror_attr_policy(
    name = "mirror_library_srcs_to_test",
    from_kind = "rust_library",
    to_kind = "rust_test",
    attr = "srcs",
)
```

But it should land only after we design the smallest safe package-level peer
rule read API.

## Non-goals

- no Orion-style prepare/analyze/declare pipeline
- no generic matcher algebra
- no arbitrary BUILD AST mutation
- no user-configured mounts yet
- no inline policy programming model in BUILD comments

The intent is still the same: real Starlark, but a pocketknife rather than a
cockpit.
