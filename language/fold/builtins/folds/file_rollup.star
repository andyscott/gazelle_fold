"""Fold child filegroups into recursive ancestor filegroups."""

load("std:lib/filegroup.star", "filegroup")


def _apply(ctx):
    local_name = ctx.params["local_name"]
    recursive_name = ctx.params["recursive_name"]

    local_files = ctx.matching_files(include = ctx.params["include"])
    outputs = [
        filegroup(
            name = local_name,
            srcs = local_files,
            present = local_files != [],
        ),
    ]

    children = ctx.child_exports("file_rollup")
    if not children.complete:
        return outputs

    recursive_srcs = []
    if local_files:
        recursive_srcs.append(":" + local_name)
    recursive_srcs.extend(children.labels)

    outputs.append(
        filegroup(
            name = recursive_name,
            srcs = recursive_srcs,
            present = recursive_srcs != [],
            visibility = ["//visibility:public"],
        ),
    )
    if recursive_srcs:
        outputs.append(
            gazelle_fold.export(
                name = "file_rollup",
                label = ":" + recursive_name,
            ),
        )
    return outputs


gazelle_fold.fold(
    name = "file_rollup",
    params = {
        "include": gazelle_fold.param(type = "strings", required = True),
        "local_name": gazelle_fold.param(type = "string", required = True),
        "recursive_name": gazelle_fold.param(type = "string", required = True),
    },
    apply = _apply,
)
