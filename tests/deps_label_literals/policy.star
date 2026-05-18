def _apply(ctx, rule):
    if not rule.matches_kind(ctx.params["kinds"]):
        return
    scan = rule.deps.label_literals_matching(
        patterns = ctx.params["deny"],
        allowed_calls = ctx.params["allowed_calls"],
    )
    if scan.matches:
        ctx.report_violation("forbidden literal deps: " + ", ".join(scan.matches))
    elif not scan.complete:
        ctx.report_violation("cannot fully inspect deps expression for forbidden literal evidence")

gazelle_fold.policy(
    name = "literal_forbidden_deps",
    params = {
        "allowed_calls": gazelle_fold.param(type = "strings", default = []),
        "deny": gazelle_fold.param(type = "strings", required = True),
        "kinds": gazelle_fold.param(type = "strings", required = True),
    },
    apply = _apply,
)
