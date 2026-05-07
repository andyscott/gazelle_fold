"""Helpers for file-rollup folds."""

load("std:lib/filegroup.star", "filegroup")


def file_rollup_fold(name, include = None, local_name = None, recursive_name = None):
    def _apply(ctx):
        active_include = ctx.params.get("include", include)
        active_local_name = ctx.params.get("local_name", local_name)
        active_recursive_name = ctx.params.get("recursive_name", recursive_name)

        local_files = ctx.matching_files(include = active_include)
        outputs = [
            filegroup(
                name = active_local_name,
                srcs = local_files,
                present = local_files != [],
            ),
        ]

        children = ctx.child_exports(name)
        if not children.complete:
            return outputs

        recursive_srcs = []
        if local_files:
            recursive_srcs.append(":" + active_local_name)
        recursive_srcs.extend(children.labels)

        outputs.append(
            filegroup(
                name = active_recursive_name,
                srcs = recursive_srcs,
                present = recursive_srcs != [],
                visibility = ["//visibility:public"],
            ),
        )
        if recursive_srcs:
            outputs.append(
                gazelle_fold.export(
                    name = name,
                    label = ":" + active_recursive_name,
                ),
            )
        return outputs

    params = {}
    if include == None:
        params["include"] = gazelle_fold.param(type = "strings", required = True)
    else:
        params["include"] = gazelle_fold.param(type = "strings", default = include)
    if local_name == None:
        params["local_name"] = gazelle_fold.param(type = "string", required = True)
    else:
        params["local_name"] = gazelle_fold.param(type = "string", default = local_name)
    if recursive_name == None:
        params["recursive_name"] = gazelle_fold.param(type = "string", required = True)
    else:
        params["recursive_name"] = gazelle_fold.param(type = "string", default = recursive_name)

    gazelle_fold.fold(
        name = name,
        params = params,
        apply = _apply,
    )
