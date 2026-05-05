"""Helpers for direct-dependency policies."""


def forbidden_deps_policy(name, kinds = None, deny = None):
    def _apply(ctx, rule):
        active_kinds = ctx.params["kinds"] if kinds == None else kinds
        if not rule.matches_kind(active_kinds):
            return
        denied = rule.deps_matching(
            patterns = ctx.params.get("deny", deny),
        )
        if denied == None:
            ctx.report_violation("cannot validate forbidden deps because deps is not a literal string list")
        elif denied:
            ctx.report_violation("forbidden deps: " + ", ".join(denied))

    params = {}
    if deny == None:
        params["deny"] = gazelle_fold.param(type = "strings", required = True)
    else:
        params["deny"] = gazelle_fold.param(type = "strings", default = deny)
    if kinds == None:
        params["kinds"] = gazelle_fold.param(type = "strings", required = True)

    gazelle_fold.policy(
        name = name,
        params = params,
        apply = _apply,
    )
