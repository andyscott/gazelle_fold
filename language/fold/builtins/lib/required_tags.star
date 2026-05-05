"""Helpers for required-tags rewrites."""


def required_tags_rewrite(name, kinds = None, tags = []):
    def _apply(ctx, rule):
        active_kinds = ctx.params["kinds"] if kinds == None else kinds
        if not rule.matches_kind(active_kinds):
            return
        rule.ensure_list_attr_contains(
            name = "tags",
            values = ctx.params.get("tags", tags),
        )

    params = {
        "tags": gazelle_fold.param(type = "strings", default = tags),
    }
    if kinds == None:
        params["kinds"] = gazelle_fold.param(type = "strings", required = True)

    gazelle_fold.rewrite(
        name = name,
        params = params,
        apply = _apply,
    )
