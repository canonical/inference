# AGENTS.md

## Code comments

Avoid comments. Code should be clear enough from naming and structure that it
does not need explaining. Do not add doc comments on functions, types, or
variables just to restate what their name already says.

Only add a comment when it is crucial for understanding the code, for
example a non-obvious constraint or a "why" that cannot be inferred from the
code itself. Keep such comments short.

When in doubt, leave the comment out.

## CLI help text

Use POSIX/Unix command synopsis syntax to describe arguments and values in
flag/command help text: `<arg>` for required values, `[arg]` for optional
values, `a|b` for mutually exclusive alternatives, and `...` for repetition.

For example: `"output format [table|json]"`.

## Decoupling external output from internal types

When serializing data for user-facing output (e.g. JSON printed by a CLI
command), do not marshal internal package types directly. Define a
dedicated output struct in the `cmd` package with its own JSON tags, and
copy fields from the internal type into it.

This keeps the external contract with the user stable even as internal
struct types evolve for unrelated reasons.
