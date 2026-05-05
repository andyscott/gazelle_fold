# Example definitions

These examples show the three useful moves in `gazelle_fold`: fold child state
into ancestor targets, modify local rules, and enforce local invariants. Most
users can import the built-in stock definitions directly:

```python
# gazelle:fold import("std:folds/file_rollup.star")
# gazelle:fold import("std:rewrites/required_tags.star")
# gazelle:fold import("std:policies/forbidden_deps.star")
# gazelle:fold use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
# gazelle:fold use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:fold use("forbidden_deps", scope = "app/...", kinds = ["rust_library"], deny = ["//legacy/..."])
```

`definitions.star` shows the next step up: a repo-owned entrypoint that loads the
built-in helper library and registers opinionated local fold, rewrite, and
policy names.

`file_rollup` is the most fold-shaped example: each package contributes local
files, ancestors combine child exports, and a full walk can build a recursive
target back toward the root.

For one-off exceptions, keep the reason beside the rule:

```python
# gazelle:fold skip("required_tags", reason = "vendored target")
rust_library(name = "vendored")
```
