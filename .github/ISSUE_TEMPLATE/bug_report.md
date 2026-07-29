---
name: Bug report
about: Something behaves differently than it says it does
title: ''
labels: bug
assignees: ''
---

Please check [LIMITATIONS.md](../../LIMITATIONS.md) first. A lot of surprising behaviour is
written down in there on purpose, with the reasoning. If your problem is listed and you think the
tradeoff is wrong, say so here anyway; that is a useful issue.

**Version**

Paste the output of `canopy version`. It prints the version, the commit and the build date, and
which of the three are set says how the binary was built.

```
canopy ... (commit ..., built ...)
```

**Environment**

- OS and version:
- Terminal:
- Provider and model, if the problem involves one:

**What happened**

**What you expected instead**

**Steps to reproduce**

1.
2.
3.

**Anything else**

Relevant output, the `canopy.json` for the project if tests or commands are involved, and whether
it happens every time or only sometimes. Redact your API keys; Canopy cannot redact what a child
process printed for you.
