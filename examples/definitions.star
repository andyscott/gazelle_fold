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
