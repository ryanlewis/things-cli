---
layout: home
title: things-cli
---

A small Go CLI for [Things3](https://culturedcode.com/things/) on macOS.
Reads tasks, projects, areas and tags straight from the Things3 SQLite
database (read-only) and writes via the `things:///` URL scheme and
AppleScript — so the app stays the source of truth and your data never
leaves the machine.

<div class="github-cta d-flex flex-wrap gap-2 my-4">
  <a class="btn btn-primary" href="https://github.com/{{ site.repository }}" target="_blank" rel="noopener">
    <i class="fa-brands fa-github"></i> View on GitHub
  </a>
  <a class="btn btn-outline-primary" href="https://github.com/{{ site.repository }}/releases/latest" target="_blank" rel="noopener">
    Latest release
  </a>
</div>

**AI-friendly by design.** Every command speaks JSON (`-j` / `--json`)
for clean piping into `jq`, agents, or scripts. A bundled agent skill
ships in the binary itself — `things skill install claude` drops it into
Claude Code, and `things skill show` prints the neutral source so you
can append it to whatever your agent reads for instructions.

## Quickstart

```sh
# What's on today
things

# Inbox, upcoming, anytime — all built-in views
things inbox
things upcoming -t urgent

# Capture a task
things add "Buy milk" --when today --tags errand,shopping

# Show, edit, complete
things show 3
things edit 3 --add-tags urgent --deadline 2026-05-01
things complete 3

# Reveal in the Things app
things open today
```

Every command takes `-j` / `--json` for structured output:

```sh
things upcoming --json | jq '.[] | select(.deadline)'
```

## Where next

- [**Install**]({{ '/install/' | relative_url }}) — one-line script, `go install`, or prebuilt binaries
- [**Commands**]({{ '/commands/' | relative_url }}) — full command reference
- [**About**]({{ '/about/' | relative_url }}) — how it works, design, AI-friendly story
- [GitHub](https://github.com/{{ site.repository }}) · [Issues](https://github.com/{{ site.repository }}/issues) · [Releases](https://github.com/{{ site.repository }}/releases/latest)
