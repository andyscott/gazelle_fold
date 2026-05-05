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
