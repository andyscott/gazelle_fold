"""Helpers for file-rollup policies."""


def file_rollup_policy(name, include = None, local_name = None, recursive_name = None):
    def _apply(ctx):
        active_include = ctx.params.get("include", include)
        active_local_name = ctx.params.get("local_name", local_name)
        active_recursive_name = ctx.params.get("recursive_name", recursive_name)

        local_files = ctx.matching_files(include = active_include)
        if local_files:
            ctx.ensure_filegroup(
                name = active_local_name,
                srcs = local_files,
            )
        else:
            ctx.remove_filegroup(name = active_local_name)

        children = ctx.child_exports(name)
        if not children.complete:
            return

        recursive_srcs = []
        if local_files:
            recursive_srcs.append(":" + active_local_name)
        recursive_srcs.extend(children.labels)

        if recursive_srcs:
            ctx.ensure_filegroup(
                name = active_recursive_name,
                srcs = recursive_srcs,
                public = True,
            )
            ctx.export(
                name = name,
                label = ":" + active_recursive_name,
            )
        else:
            ctx.remove_filegroup(name = active_recursive_name)

    params = {}
    if include == None:
        params["include"] = gazelle_policy.param(type = "strings", required = True)
    else:
        params["include"] = gazelle_policy.param(type = "strings", default = include)
    if local_name == None:
        params["local_name"] = gazelle_policy.param(type = "string", required = True)
    else:
        params["local_name"] = gazelle_policy.param(type = "string", default = local_name)
    if recursive_name == None:
        params["recursive_name"] = gazelle_policy.param(type = "string", required = True)
    else:
        params["recursive_name"] = gazelle_policy.param(type = "string", default = recursive_name)

    gazelle_policy.package_policy(
        name = name,
        params = params,
        apply = _apply,
    )
