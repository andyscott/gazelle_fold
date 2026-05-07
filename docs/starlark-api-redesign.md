# Starlark API design

## Product thesis

`gazelle_fold` should be understood first as a bottom-up fold over the BUILD
tree, not as a generic policy engine. Fold callbacks run as Gazelle visits
packages, may export state upward, and can synthesize parent-facing targets from
child exports. Rewrites and policies are the local companions: rewrites modify
targets in the package currently being visited, while policies validate them.

That seam is especially valuable in large monorepos, and more so when AI agents
are authoring BUILD files alongside humans. Repo owners can encode local naming,
aggregation, and dependency conventions once, then let ordinary Gazelle runs
pull machine-authored edits back toward the project's own patterns.

## Recommendation

The public surface should have four layers:

```text
BUILD directives       activate named definitions by package scope
std:folds/*            ready-made folds users can import directly
std:rewrites/*         ready-made rewrites users can import directly
std:policies/*         ready-made policies users can import directly
std:lib/*              tiny output helpers that stay clearer than raw rule calls
gazelle_fold host      a tiny safe runtime for custom folds, rewrites, and policies
```

Module paths resolve through mounts rather than Bazel labels:

```text
std:<path>    bundled gazelle_fold standard library
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

- stock definitions are importable directly
- helper modules are explicit and always loadable
- user-authored modules are normal mounted files
- the Go host owns the unsafe BUILD/Gazelle machinery
- the central abstraction is still the BUILD-tree fold, not a miniature general
  build language hidden in comments

## Golden path

```python
# gazelle:fold import("std:folds/file_rollup.star")
# gazelle:fold import("std:rewrites/required_tags.star")
# gazelle:fold use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
# gazelle:fold use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
```

An activation nearer the target package layers over farther activations, so a
child can override one field without repeating the entire contract:

```python
# gazelle:fold use("required_tags", scope = ".", tags = ["team:child"])
```

## Bundled modules

### Importable stock definitions

```text
std:folds/file_rollup.star
std:rewrites/required_tags.star
std:policies/forbidden_deps.star
```

These register generic definition names driven entirely by `use(...)` params.

### Loadable helper library

```python
load("std:lib/filegroup.star", "filegroup")
```

Custom folds, rewrites, and policies are ordinary repo-owned `.star` modules.
The stdlib keeps only tiny helpers whose call sites stay clearer than raw host
calls. For package-local filegroup synthesis:

```python
load("std:lib/filegroup.star", "filegroup")

def apply(ctx):
    docs = ctx.matching_files(include = ["*.md"])
    return [
        filegroup(
            name = "docs",
            srcs = docs,
            present = docs != [],
        ),
    ]

gazelle_fold.fold(
    name = "markdown_docs",
    apply = apply,
)
```

Repo-owned entrypoints can be imported from the `root` mount:

```python
# gazelle:fold import("root:build/gazelle_fold/rust.star")
```

Inside a `.star` file, plain paths stay relative to the importing module:

```python
load("helpers.star", "helper")
load("../shared/common.star", "common")
```

## Minimal host API

```python
gazelle_fold.param(type, required = False, default = None)
gazelle_fold.fold(name, params = {}, apply = fn)
gazelle_fold.rewrite(name, params = {}, apply = fn)
gazelle_fold.policy(name, params = {}, apply = fn)
gazelle_fold.rule(kind, name, present = True, attrs = {})
gazelle_fold.export(name, label)
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

### Rewrite callbacks

```python
def apply(ctx, rule):
    ...
```

```text
ctx.rel
ctx.name
ctx.params

rule.kind
rule.name
rule.matches_kind(patterns)
rule.list_attr(name)
rule.ensure_list_attr_contains(name, values)
rule.deps_matching(patterns)
```

Matching is intentionally an activation-time concern, not a registration-time
constraint. That is what lets stock definitions be imported directly and configured
through `use(...)`.

### Policy callbacks

Policies receive the same `(ctx, rule)` shape as rewrites, plus the one operation
that makes them policies:

```text
ctx.report_violation(message)
```

### Fold callbacks

Fold callbacks are the tree steps. Each package can inspect local files, combine
exports from already-visited children, generate or remove local targets, and
export a label upward for ancestors.

```python
def apply(ctx):
    ...
```

```text
ctx.rel
ctx.name
ctx.params
ctx.matching_files(include)
ctx.rules_matching(kinds)
ctx.child_exports(name)
```

Fold callbacks describe package outputs declaratively:

```python
def apply(ctx):
    deps = [":" + rule.name for rule in ctx.rules_matching(["rust_library"])]
    return [
        gazelle_fold.rule(
            kind = "rust_clippy",
            name = "clippy",
            present = deps != [],
            attrs = {"deps": deps},
        ),
    ]
```

Returned `gazelle_fold.rule(...)` values declare one managed output's desired
presence regardless of rule kind. The host reconciles `present = True` and
`present = False`; omission is deliberately a no-op so a fold cannot accidentally
delete a package rule it never named. `attrs` accepts literal bools, strings, and
lists or tuples of strings. For example, `filegroup` is just another rule kind
whose `srcs` live in `attrs`; `srcs = []` is still a valid value, so deletion
stays explicit through `present = False` rather than hiding behind an empty list.
`gazelle_fold.export(...)` is ephemeral: it makes a label visible to ancestor
folds during this walk but does not mutate a BUILD file directly.

The stock `file_rollup` fold now follows the same contract through a small local
helper: it returns `gazelle_fold.rule(...)` outputs for the local and recursive
filegroups, and a `gazelle_fold.export(...)` output only when there is a recursive
label for ancestors to consume.

That declarative boundary is fold-specific on purpose. Folds own a package-level
desired state, so returning outputs makes the whole package shape visible at
once. Rewrites and policies already run at the natural one-rule boundary, where
`rule.ensure_list_attr_contains(...)` and `ctx.report_violation(...)` stay
smaller and clearer than inventing patch or violation result objects.

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
- anchored skips
- leaf-to-root package-tree traversal and partial-walk completeness
- safe BUILD AST mutations
- diagnostics and provenance

### Starlark library

- stock fold, rewrite, and policy entrypoints
- tiny output helpers where they remove real call-site noise
- later helpers only when they earn their keep through repeated use

## The current helper

### `filegroup(...)`

```python
def filegroup(name, srcs, present = True, visibility = None):
    ...
```

This is stdlib sugar over `gazelle_fold.rule(kind = "filegroup", ...)`, not a
special host concept. It keeps the runtime target-agnostic while giving common
file-based folds a compact call site. `present = False` expresses deletion
explicitly, and `visibility` forwards literal BUILD visibility labels when the
generated target needs them.

## Why `mirror_attr` should still be separate

File aggregation reasons about files and child exports. Attribute mirroring
reasons about peer rules in one package. Those are different problems even when
both mention `srcs`.

A future helper can likely be:

```python
mirror_attr_rewrite(
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
- no inline definition programming model in BUILD comments

The intent is still the same: real Starlark, but a pocketknife rather than a
cockpit—purpose-built for folding project-local build knowledge back through a
large tree.
