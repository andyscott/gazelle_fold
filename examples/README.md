# Example policies

Most users can import the built-in stock policies directly:

```python
# gazelle:policy import("std:policies/required_tags.star")
# gazelle:policy import("std:policies/file_rollup.star")
# gazelle:policy import("std:policies/forbidden_deps.star")
# gazelle:policy use("required_tags", scope = "...", kinds = ["rust_library"], tags = ["team:runtime"])
# gazelle:policy use("file_rollup", scope = "...", include = ["*.rs", "BUILD.bazel"], local_name = "all_sources", recursive_name = "all_sources_recursive")
# gazelle:policy use("forbidden_deps", scope = "app/...", kinds = ["rust_library"], deny = ["//legacy/..."])
```

`policies.star` shows the next step up: a repo-owned entrypoint that loads the
built-in helper library and registers opinionated local policy names.

For one-off exceptions, keep the reason beside the rule:

```python
# gazelle:policy exempt("required_tags", reason = "vendored target")
rust_library(name = "vendored")
```
