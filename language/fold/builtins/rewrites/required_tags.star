"""Ensure selected rules carry caller-supplied tags."""


def _apply(ctx, rule):
    if not rule.matches_kind(ctx.params["kinds"]):
        return
    rule.ensure_list_attr_contains(
        name = "tags",
        values = ctx.params["tags"],
    )


gazelle_fold.rewrite(
    name = "required_tags",
    params = {
        "kinds": gazelle_fold.param(type = "strings", required = True),
        "tags": gazelle_fold.param(type = "strings", default = []),
    },
    apply = _apply,
)
