# Example definitions

These examples show the three useful moves in `gazelle_fold`: fold child state
into ancestor targets, modify local rules, and enforce local invariants. Most
users can import the built-in stock definitions directly:

```python
# gazelle:fold import("std:folds/filegroup_rollup.star")
# gazelle:fold import("std:rewrites/required_tags.star")
# gazelle:fold import("std:policies/forbidden_deps.star")
# gazelle:fold use("filegroup_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
# gazelle:fold use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:fold use("forbidden_deps", scope = "app/...", kinds = ["rust_library"], deny = ["//legacy/..."])
```

`filegroup_rollup` is the canonical fold shape: each package contributes local
files, ancestors combine child exports, and a full walk builds a recursive target
back toward the root.

`filegroup/` is the smallest stock-fold example. It uses `filegroup_rollup` to
maintain local markdown filegroups and roll a recursive target back toward the
root.

`rust_clippy/` is the package-local synthesis example. One activation at the
subtree root derives a managed `rust_clippy(name = "clippy")` target in every
package that owns handwritten Rust rules, and removes that managed rule when a
package stops owning any. That is the right shape when every package needs the
same local aggregator, but the source of truth should remain the package's own
rules rather than a parent rollup.

For one-off exceptions, keep the reason beside the rule:

```python
# gazelle:fold skip("required_tags", reason = "vendored target")
rust_library(name = "vendored")
```
