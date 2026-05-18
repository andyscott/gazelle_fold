"""Reject direct dependencies that match caller-supplied deny patterns."""


def _apply(ctx, rule):
    if not rule.matches_kind(ctx.params["kinds"]):
        return
    denied = rule.deps.labels_matching(patterns = ctx.params["deny"])
    if denied == None:
        ctx.report_violation("cannot validate forbidden deps because deps is not a valid literal label list")
    elif denied:
        ctx.report_violation("forbidden deps: " + ", ".join(denied))


gazelle_fold.policy(
    name = "forbidden_deps",
    params = {
        "deny": gazelle_fold.param(type = "strings", required = True),
        "kinds": gazelle_fold.param(type = "strings", required = True),
    },
    apply = _apply,
)
