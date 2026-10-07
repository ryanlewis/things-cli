---
title: Commands
url: /commands/
weight: 2
eyebrow: Commands
description: "Full command reference for things-cli: listing, searching, capturing, editing, completing and the bundled agent skill."
---

Read commands (`list`/views, `projects`, `areas`, `tags`, `show`, `search`)
accept `-j` / `--json` for structured output. Run `things --help` or
`things <subcommand> --help` for the full flag list.

In JSON, `status`, `type` and `start` are string enums rather than the raw
Things integers. `status` is `"open"`, `"completed"` or `"cancelled"`, and
appears on tasks, projects and checklist items. `type` is `"task"` or
`"project"`, and appears on task rows only — `projects`, `areas` and `tags`
rows carry no `type`. Headings are never returned by any command, so the
third Things type never reaches the output.

`start` is `"inbox"`, `"anytime"` or `"someday"`, and appears on task and
project rows. It is the list an item falls back to when it carries no date,
so it does not on its own say which list the app shows the item in: a dated
`"anytime"` row is in Today, a `"someday"` row dated after today is in
Upcoming, and only an undated one is in Someday. Just after midnight,
Things can leave an open task scheduled for the new day in Someday until it
next tidies up; the app already shows it as today's, so it reports
`"anytime"`, as it will once Things moves it. An undated `"anytime"` task whose
deadline is today or past is in Today as well, and an undated `"inbox"` one
is in Today and Anytime instead of the Inbox.

In v0.7.0 and earlier `type` and `start` were both integers, so a caller
matching on `.type==1` has to become `.type=="project"`, and one matching
on `.start==2` has to become `.start=="someday"`. `startBucket` alongside
them is still an integer: `1` is the app's This Evening section and `0` is
everything else. Only the first of those has a name in Things' own
vocabulary, so naming the pair would have meant inventing a word for `0`.

The `type` values a listing reports are not the ones an `import` payload
takes: that payload is Things' own JSON URL scheme, which spells a task
`"to-do"`. The payload is the one place these pages use Things' word rather
than the CLI's, because it is passed through untouched. Do not copy `.type`
from a listing into an import item.

## Listing

```sh
things                 # today's tasks (default view)
things list <view>     # explicit form — see views below
things <view>          # shortcut: things inbox, things today, etc.
```

Available views: `today`, `inbox`, `upcoming`, `anytime`, `someday`,
`repeating`, `logbook`, `trash`, `deadlines`.

Every view above except `inbox` and `anytime` lists projects as well as tasks,
marked `(project)` in plain output and `"type": "project"` in JSON; `--area`
and `--tag` match a project, `--project` never does, and `things projects` is
how to sweep projects on their own. The bundled agent skill states the rule
and the reasoning in full — `things skill show`.

`today`, `anytime` and `someday` are arranged the way the app arranges them —
unfiled items first, then areas, and inside an area its own loose tasks before
its projects' — so plain output prints each project name once as a group header
above its tasks. `anytime` carries no project rows of its own because every
active project is trivially "anytime": listing them all would bury the tasks,
so the app uses each project as a group header instead. Like the app, it
leaves out the tasks of a project in Someday or scheduled for a later date,
including those under one of its headings and those Today shows; `today` and
`upcoming` still list theirs. `today` and `someday`
group the same way but do list their project rows, because a project put in
Today or Someday has actually been put somewhere. `someday` reaches only the
first half of the arrangement: it carries no task with a parent project, so it
ends at unfiled items and then areas.
`today` then orders within a group the way the app does: by the day an item
was last placed in Today, most recent first, so items placed today come above
those carried over from an earlier day, and then by the position Things keeps
for the day. An item closed today stays where it was rather than moving to the
end. `upcoming` reads by date instead, the way the app's own Upcoming does.
Like the app, it also lists an undated Anytime task whose deadline is still to
come, under the deadline's day, and `--on`/`--from`/`--to` match it on that
day. `today` likewise lists an undated task in the Inbox or Anytime once its
deadline is today or past, unless it was taken out of Today for that deadline.
Such a task also comes back from `anytime`, from the Inbox as well as from
Anytime, as in the app, so merge a sweep on `uuid`. One from the Inbox
leaves `inbox` while Today holds it, as it leaves the app's Inbox. `--on`/`--from`/`--to` match it on today, the day Today
shows it.

A bare `--project` lists the project the way its page in the app does: the
tasks under no heading first, then each heading's in heading order. Within
each, Anytime tasks come first, then scheduled tasks by date, then Someday
tasks. A bare `--area` or `--tag` arranges each project's tasks the same way.

`logbook` is everything closed, not just everything finished. Cancelling a
task or a project logs it under its stop date beside the completed ones, the
way the app's Logbook shows both, so the view returns cancelled rows too.
`status` tells them apart — `"completed"` or `"cancelled"` in JSON, `[x]` and
`[~]` in plain output — so filter on it when you mean finished rather than
closed: `things logbook -j | jq '.[] | select(.status=="completed")'`.

An item you tick off in Today is not in `logbook` yet. Things keeps it under
Today for the rest of the day, ticked, and files it into the Logbook when the
day rolls over, or sooner if you run `things log` — the app's "Log Completed
Now", which files the day's closed items straight away. `things today` lists
it too, in place among the open tasks, marked `[x]` (or `[~]` if cancelled)
in plain output and carrying `status` in JSON. That is the app's default "Move
completed items to Logbook: Daily" setting, and the CLI reads the setting: set
to Immediately, nothing is held and every closed item is in `logbook` at once;
set to Manually, closed items stay where they were, whatever day they closed,
until `things log`. `inbox`, `anytime`, `upcoming` and `someday` behave the
same way, because the app goes on showing a just-closed item there too.
`upcoming` keeps only what was in it while open: a task closed ahead of its
date, or an undated one with a deadline after today.

`--open-only` leaves those closed items out, for when you want only the work
still to do: `things today --open-only`. It works on every listing, and changes
nothing on the ones that list only open tasks anyway (`deadlines`, `repeating`,
a bare `--tag` sweep); `logbook` and `trash` reject it.
To make it the default, set `open_only = true` in the
[config file](/configuration/); `--open-only=false` then lists the closed
items for one call, and `logbook` and `trash` ignore the setting.
`--include-completed`, which used to be how to ask for the closed items, is
still accepted. It has no effect except to override `open_only`, as
`--open-only=false` does.

`logbook` holds nothing Things has not logged yet, wherever it was closed, as
the app's Logbook does. A closed item is therefore either in `logbook` or
still in place, never both. Still in place, it is listed by its view, by its
project (`things --project <uuid>`), or by its area (`things --area <name>`).
The one exception is a closed Anytime project with no area: no list shows it,
in the app or the CLI, and `things projects --completed` is where to find it
until it is logged. The lists overlap each other, since a task scheduled for
today is in the Anytime bucket too and an undated one due later is in both
`anytime` and `upcoming`, so sweeping them means merging on `uuid`. The tasks
of a project closed today stay in place in these lists, struck through, until
the project is logged; then they fold into its row, as the next paragraphs say.
A task closed today inside a project in Someday or scheduled for later is in no
list, as in the app, which shows it only on the project's page: `things
--project <uuid>` lists it. With a filter on a view, `things today -p "Launch
v2"` returns the tasks of a closed "Launch v2" that closed today, rather than
nothing.

A bare `--project`, with no view, is the app's project page: `things -p
"Launch v2"` lists an open project's tasks plus every one of them closed today
and not yet logged, struck through in the app's project page until the day
rolls over, whichever list it was closed out of. A project's contents are not
one of the lists above, so they overlap them.

A bare `--area` is the area's page the same way: `things -a Work` lists the
area's tasks and projects, closed today and not yet logged among them, as the
app's area page shows them, and its projects' tasks, closed today among them,
as each project's page does. A project closed today is one row there, as in
`logbook`. A bare `--tag` sweep lists open tasks only: a tag is a filter in the
app, not a list with a page of its own, so name the view to see the closed
ones too: `things today -t urgent`.

A closed project is one row in `logbook`, not a row plus its contents. The
app folds a closed project's tasks into the project's own row and lists none
of them separately (and so do the other lists, once the project is logged), and `trash` does the same for a trashed project. To reach
those tasks, name the project: `things --project <uuid>` on a closed or
trashed project returns its contents whatever their status, which is what the
app answers for the same question. Naming the project works inside a view as
well as in that bare form: `things anytime --project <uuid>` on a trashed
project lists its open tasks, and naming a closed project on `today` or
`anytime` lifts the fold there too, so a slice of those contents is reachable
without leaving the view. A task you threw away
out of a project is the exception — it keeps its own `trash` row, because it
is in the Trash on its own account rather than through its project. A task
thrown away out of a project that is itself in the Trash is reachable nowhere,
as in the app.

`someday` is the app's Someday list: the deferred things you have not filed
under a project. A task inside a project stays inside it however it is
deferred, so `someday` returns Someday projects and unparented Someday tasks,
not the deferred tasks of an Anytime project. Open the project to see those —
`things --project "Name"` — or use `anytime`, which carries the project itself.
Because nothing in `someday` has a parent project, `--project` there could
never match; the CLI rejects the combination rather than print an empty list.

`repeating` lists repeating task and project templates. The items a template
generates are ordinary tasks and projects and appear in `today`, `upcoming`,
`things projects` and the rest; the template itself appears only here — plus
`trash` or `logbook` for a template that ends up there, since those two report
what the database holds. Both carry projects, so a trashed or logged project
template shows there.
Project templates are marked `(project)` in plain output and carry
`"type": "project"` in JSON.

The tasks inside a project template are hidden along with it, since they
would otherwise list against a project `things projects` does not report.
`trash` and `logbook` still show them once they are trashed or closed.
Naming a project template with `--project` therefore lists nothing on those
views, and the CLI prints a one-line note on stderr saying so. Under `--json`
the note stays on stderr, so stdout is still an empty array. On `trash`,
`logbook` and `repeating`, which keep templates, there is no note: an empty
listing there means nothing has been trashed or closed yet.

A task inside a project template counts as repeating: it carries
`"repeating": true`, and `edit`, `complete`, `cancel` and `import` refuse
`when`, `deadline` and status changes on it as on the template. The tasks
inside a project Things generated from the template are ordinary.

`things search` is a lookup rather than a view, so it returns templates too.
Results carry `"repeating": true`.

Filter any list with `-p/--project`, `-a/--area`, or `-t/--tag`. Names
ignore case and surrounding spaces. Case is ignored the way Things ignores it,
so `-t STRASSE` finds a tag named
`Straße`. An accented letter matches whether it was typed as one character or
as a letter plus a combining accent, as in Things, but never the bare letter:
`-t Cafe` does not find `Café`. Project and area names, but not tag names,
also match across Unicode compatibility forms, as in Things: `-a Ｗork`
(fullwidth) or `-p "Q²"` finds `Work` or `Q2`, and a no-break space matches a
space. A project, area or tag name typed in
its exact case lists only that one, even when another title differs from it
only by case. On their
own the filters cover everything open in the project, area, or tag, plus
what a project or area closed today and Things still shows there — so
`things -a Work` lists that area's own projects as well as its tasks, while
`-p` still returns a project's contents rather than the project row. Add a
view and the filter applies within it, with the view named in the output:

```sh
things -p "Launch v2"                # every open task in the project, plus those closed today
things today -p "Launch v2"          # today's slice of it, labelled "view: today"
things upcoming -t urgent
things anytime --area "Side projects"
things --json list today | jq '.[] | .title'
```

Tasks filed under a project heading belong to that project, so they appear
under `-p` and under the project's area.

A task row's date is its deadline (`due:2026-10-02`) if it has one,
otherwise its start date. A task with both also shows its start date,
before the deadline, when the terminal is wide enough.

A task with a checklist shows its progress after the title, as the items
done out of all of them (`✓ 2/5`, where done means completed or
cancelled). Things.app shows only a checklist icon on the row, not the
counts. `things show` lists the items.

Plain task lists fit the terminal width. When a row is too wide, the
checklist progress goes first, then that extra start date. If the row is
still too wide, a title longer than 40 columns is cut to 40 with `…`, so
one long title does not cost every row its tags or date. Next the tags
switch to a short form, the first tag and a count of the rest
(`[waiting-on-pos… +2]`, or `[waiting-on-pos…]` for a lone long tag),
and the dates to one relative to today
(`today`, `tomorrow`, `due:Fri`, `3d ago`, `2 Oct`, with the year only
when it is not this one). Only if that still does not fit do the tags go,
and then the date. Then the title is cut further, down to 10. A project
keeps its `(project)` marker; the cut comes out of the title before it.
Group headers are cut to fit too. Titles and headers are cut, and tags
and dates shortened, only on a terminal. Piped output never cuts or
shortens anything, but when any row is wider than 120 columns, whole
columns still go from every row, in the same order: the checklist
progress, the extra start date, the tags, then the date. `--json`
carries every field in full.

A list prints each task on one line: a line break or a tab in a title,
tag, project, area or group header shows as a space. `things show` keeps
a title's own line breaks, and `--json` carries it exactly as written.

`things projects`, `things areas`, and `things tags` list the
collections themselves. `things projects` accepts `--area`, which takes
a name the way the list filters do, and `--completed`.

On a terminal, `things projects` fits the width the same way: a title
longer than 30 columns is cut first, then, if that is not enough, an area
longer than 20. Then the tags go, then the area, and then the title is
cut down to 10. Piped output keeps every column whole.

Projects are scheduled the same way tasks are, and `things projects -j`
reports that with the same field names and encodings: `start`,
`startBucket`, `startDate` and `deadline`. A caller can tell a scheduled
project from an anytime one without a `things show` per project.

`things projects -j` also reports two counts per project. `taskCount` is
every untrashed task in the project; `openCount` is the ones still open.
The difference is the ones no longer open, which means completed or
cancelled. Tasks filed under a project heading count towards both; the
heading rows themselves never do, and neither do trashed tasks or
checklist items. Both numbers are Things' own bookkeeping, read straight
from the database rather than recounted by the CLI.

That makes it one call to find projects whose work has landed but which
are still open:

```sh
things projects -j | jq '.[] | select(.openCount == 0 and .taskCount > 0)'
```

`taskCount > 0` keeps out empty projects, which have nothing done rather
than everything done. It does not tell done from cancelled: a project
whose tasks were all cancelled matches the same filter. Plain output
marks the same projects with a filled `●` progress icon, and under
`--completed` that icon also marks every completed project, including
empty ones. A project holding a repeating task never appears while the
repeat is live: Things counts the hidden template row itself as an open
task, and a template never completes. `things list -p <project>` hides
that template, so it can report no open tasks for a project whose
`openCount` is 1.

A task row with a checklist carries `checklistProgress`: `total` is every
item on the checklist and `open` the ones still open, so the difference is
the ones completed or cancelled. A row without a checklist leaves the field
out. `things show` lists the items themselves.

```sh
things list today -j | jq '.[] | select(.checklistProgress.open > 0) | .title'
```

## Inspecting a task

```sh
things show 3                 # by index from the last plain list
things show <uuid>            # by Things3 UUID
things show "Buy milk"        # by title (interactive disambiguation)
things show 3 --agent         # Markdown brief for handing to an agent
```

On a terminal, long values, notes and checklist items wrap to its width,
and each wrapped line keeps the indent its text started at. A note keeps
its own line breaks, and a word or URL breaks only when it is wider than
the space left. A line that starts with an indent or a list marker
(`- `, `* `, `• `, `1. ` or `1) `) wraps under the text after it, and tabs
are expanded to every 4th column. Piped output, `--json` and `--agent` are
not wrapped, and keep a note's tabs.

A title reference is matched as a substring, literally and
case-insensitively. There is no wildcard syntax: `%` and `_` are characters
to find, so `things show "20_30 review"` finds the task spelled with an
underscore and not the one spelled with a colon. A title typed in its exact
case wins over titles that differ from it only by case: with to-dos `Water`
and `water`, `things show water` finds the second, while `things show WATER`
matches both.

A title matching more than one item is reported rather than guessed at: an
interactive run prints the candidates and asks which one, and a non-TTY run
returns them as an error. That holds for an exact title too — a project and a
to-do that share one are both offered, instead of the lookup picking whichever
sorts first.

After any plain list or `search`, numeric indices stay valid until the
next one. A listing's order is fixed, so the same list run twice numbers
the same items the same way — but the numbers still move as items are
added, closed or rescheduled, so re-read the list rather than reusing an
index from an earlier one.

A number that is not a row in the last list is refused, not searched for as
part of a title: after a 10-row list, `things complete 12` does not complete
"Chapter 12 notes". The same goes when there is no last list at all. The error
says to re-run the list. A task whose title is exactly that number still
resolves, as does a uuid.

Only bare digits are row numbers. `+3` and `#3` are not, even when row 3
exists, and they are not searched for as part of a title either: `things
complete '#12'` does not complete "Fix issue #12". The error says to pass
the uuid, and to use `3` when the last list has a row 3. A task whose title
is exactly `#3` still resolves, and space around a number or a marked ref
is not part of the title: ` 2026 ` finds a task titled "2026".

A `--json` listing is the exception: it prints no numbers and records
none, so it leaves your indices pointing where they did. That keeps a
script or an agent running `--json` in another window from renumbering
the list you are reading. Agents should act on the `uuid` rather than an
index — see [Working with agents](/agents/).

## Handing a task to an agent

`things show <ref> --agent` prints a Markdown brief written for an agent,
with the commands that act on the item. See
[Working with agents](/agents/).

## Searching

```sh
things search "milk"
things search "release" --json
```

The query matches titles and notes literally and case-insensitively, so
`strasse` finds "Straße". There is
no wildcard syntax: `%` and `_` are characters to find, so
`things search "50%"` returns the items that say "50%".

## Capturing

```sh
things add "Buy milk"
things add "Ship the thing" --when today --tags work,urgent
things add "Pay invoice" --deadline 2026-06-01 --notes "Send PDF"
things add "Review PR" --project "things-cli"
things add "Plan offsite" --list "Open source"   # --list takes a project or area; it overrides --project if both are given
things add "Groceries" --checklist "Milk\nBread\nEggs"
```

`--when` accepts a keyword (`today`, `tomorrow`, `evening`, `anytime`,
`someday`), a date `YYYY-MM-DD`, a time `HH:MM`, a date+time
`YYYY-MM-DD@HH:MM`, or an RFC3339 timestamp. `--deadline` accepts a
`YYYY-MM-DD` date only.

Things files the to-do by matching `--list` (or `--project`) against the
titles of the projects and areas it shows, and `--heading` against the
headings of that project. Both ignore case but not surrounding space, so
`" Tools "` does not match a project called Tools. When nothing matches it
does not complain: it puts the to-do in the Inbox, or adds it to the list
without the heading. A trashed project does not match by title, nor does a
closed one Things has moved to the Logbook; one closed but not yet logged
(by default, one completed or cancelled today), which Things still shows,
does, and Things reopens it. Only the default "Move completed items to
Logbook" choice, daily, was measured; under the others the CLI assumes the
same not-yet-logged rule its lists follow. When several projects, or with
no project several areas, match the title, Things files the to-do in the
one whose UUID sorts first, even when another one's title matches exactly,
and the CLI checks that one. A UUID names one directly: the CLI sends it
to Things as `list-id`, and Things files into that project whatever its
state, reopening a closed one, or into that area. The add warns on stderr
when the to-do will not land where you asked, and notes when it will land
somewhere you may not expect, then sends it anyway:

```
warning: Things finds no project or area called "Nowhere"; it will put the to-do in the Inbox
warning: "Tools" has no heading "Later"; Things will add the to-do there without a heading
warning: --heading "Later" needs --list or --project; Things will ignore it and put the to-do in the Inbox
note: several lists are called "tools"; Things will use "Tools" (4Kc9…); pass a UUID to choose
note: several lists are called "errands" (a project and an area); Things will use the project "Errands" (8TZX…); pass a UUID to choose
note: "Tools" is completed; Things will file into it and reopen it
note: "Tools" is in the Trash; Things will file into it there
```

`things project add` creates a new project with the same flag set
(`--notes`, `--when`, `--deadline`, `--tags`, `--area`, `--todos`). `--area`
takes an area name or UUID; a UUID goes to Things as `area-id`, since
Things matches `area` by title only. It matches the title ignoring case and
compatibility forms (fullwidth letters, superscript digits) but not
surrounding space; when several areas match, Things takes the one whose
UUID sorts first, as for `add --list`, and the CLI notes which. When
nothing matches it creates the project with no area. `project add` warns on
stderr when that will happen, then sends the project anyway:

```
warning: Things has no area called "Nowhere"; it will create the project with no area
```

`add` and `project add` then find the new item in the database and print it
exactly as `things show` would, the same object under `--json`. Things
returns no UUID for a new item, so the CLI looks for an item of the right
kind with that title, created after the write started, that was not there
before it, filed where the add sent it: the project or area `--list`,
`--project` or `--area` resolved to (and the heading, if Things has it),
or no project or area when none was given or Things will not match it. A
new item with the same title somewhere else, from another command adding
it at the same moment, is not this add's. It waits as long as an edit's
read-back (five seconds by default; see `--verify-timeout` under Editing).

- If no such item appears, the command exits non-zero with `add not
  confirmed: …`. Things may have dropped it (check that Things3 is running)
  or may be slow to save. Run the `things search` command the error prints
  before retrying, so a retry does not create a duplicate. It carries
  `--db` and `--config` when the add was given them.
- If more than one new item with that title is visible together where the
  add sent it when it is read back (the same title added to the same place
  at the same moment),
  the CLI does not guess. It prints `Sent to Things, not confirmed
  (more than one new item has this title): "Buy oat milk" (uuid1, uuid2)`
  and exits 0.
- `--no-verify` (or `no_verify = true`) skips the read-back and prints
  `Sent to Things, not confirmed (--no-verify): "Buy oat milk"`. If no
  read of the database works, the item is still sent, with one warning
  (the tag check's, when `--tags` was given), and the line says
  `(database unreadable)`. A read that fails after earlier reads worked
  does not count: the add is then not confirmed, as above.

Under `--json` the unconfirmed cases print `{"title": …, "confirmed":
false, "reason": "no-verify"|"unreadable"|"ambiguous"}`, with
`"candidates": [uuids]` for `ambiguous`. There is no `uuid`, because the
add did not return one. An add that `--when` did not file where it said
(below) is an error instead: under `--json` its one object has `"error":
"misfiled"`, the item's `"uuid"` and where it `"landed"`.

## Editing

```sh
things edit 3 --title "Buy oat milk"
things edit 3 --tags shopping            # replace all tags
things edit 3 --add-tags urgent          # additive
things edit 3 --deadline 2026-05-15
things edit 3 --when tomorrow
things edit 3 --notes "From Holland & Barrett"
things edit 3 --append-checklist "Almond too"
things edit 3 --complete                 # also: --cancel, --duplicate, --reveal
things edit 3 --list Tools --heading Setup
```

`--list` and `--heading` move the to-do. Things matches them the way it
does for `add`: ignoring case and compatibility forms but not surrounding
space, and only against
the projects (including one closed but not yet logged) and areas `add`
matches; when several share the title, the one whose UUID sorts first is
the one the to-do moves to. `--list` also takes a UUID of a project or
area, which the CLI sends to Things as `list-id`; `--list-id` and
`--heading-id` take UUIDs directly. `--heading` on its own looks in the
project the to-do is already in. When nothing matches, Things does not
complain: an unknown list is ignored, so the to-do stays where it is or,
with `--heading`, the heading is looked up as if given on its own; an
unknown heading in a known list moves the to-do to that list with no
heading; an unknown heading on its own is ignored; and an unknown
`--list-id` or `--heading-id` is ignored too. `edit` warns on stderr when
that will happen, then sends the edit anyway:

```
warning: Things finds no project or area called "Nowhere"; the to-do will stay where it is
warning: Things finds no project or area called "Nowhere"; it will look for --heading "Setup" in the to-do's own project
warning: Things finds no project or area with id "abc123"; the to-do will stay where it is
warning: Things has no heading with id "abc123"; the to-do will stay where it is
warning: Things has no heading with id "abc123"; it will ignore --heading-id
warning: "Tools" has no heading "Later"; Things will move the to-do there without a heading
warning: "Tools" has no heading "Later"; Things will leave the to-do where it is
warning: --heading "Later" needs --list: the to-do is not in a project, so Things will leave it where it is
```

When the move files the to-do somewhere new, `edit` notes the same things
`add` does: several lists sharing the title and which one Things will use,
or a closed or trashed project. Things moves a to-do into a closed project,
logged or not, and reopens it, and into a trashed one by UUID, which stays
in the Trash. A project wins over an area of the same title. `--heading` on
its own moves the to-do under that heading even when its project is logged
or trashed, and leaves the project closed.

A move Things will drop counts as no change, and so does a move to the
list, area or heading the to-do is already in, since Things records no
change for it. An edit made only of such moves prints the item at once
instead of waiting for a change. When a project has headings whose titles
differ only in case, Things files the to-do under the same one of them
whichever case you send, and the check follows it.

Things reports nothing back from an edit, so the CLI waits for the item's
modification date to change (and, with `--complete` or `--cancel`, for the
status to change too; an edit with only one of those is checked on the
status alone). Things does not change the modification date when only the
checklist changes, so an edit with a checklist flag also counts once the
checklist differs from what it was before. It then prints the item the way `things show` does — the same
object under `--json`:

```sh
things edit 3 --title "Buy oat milk"
# Title:    Buy oat milk
# UUID:     8QK2xgV1m3C9p7RfT4hLwe
# Status:   Open
# …
```

If the item never changes within the read-back wait, `edit` exits
non-zero with `edit did not apply: …` — Things accepted the command and
dropped it, so check that Things3 is running. The wait is five seconds by
default; the global `--verify-timeout DURATION` flag changes it for one
run (`--verify-timeout 2500ms`, `--verify-timeout 10s`), and
`verify_timeout` in the config file changes it for every run. It must be
above zero: `--no-verify` is how to skip the wait. The same wait applies
to `add`, `project add`, `complete`, `cancel`, `tag add` and the new
items and status changes in an `import`.
An edit that sets every field to the value
it already has is detected before the wait when every flag is `--title`,
`--notes`, `--tags`, `--add-tags` (tags compared case-insensitively), a
`--deadline` date, an empty `--append-notes` or `--prepend-notes`, or a
`--when` of `anytime`, `someday`, `today`, `evening`, `tomorrow`, empty,
a date, an `HH:MM` time or a `YYYY-MM-DD@HH:MM` date and time. A past date
files the item under today. `--when today`, `evening`, today's date or a
past date clears a reminder, so on an item with one it counts as a change
and waits; a later day keeps the reminder. A time sets the reminder, for
today if the time is still to come and tomorrow if it has passed, so it
is no change only on an item with that reminder on that day. Tags that do
not exist in Things count as no change, since Things drops them, unless
`--create-tags` creates them first. So does a move the warnings above say
will leave the item where it is. It then prints the item straight away.
Any other re-set value, such as an English phrase, still waits and
reports the same error, so on that error check the item with `things
show` before retrying.

With `--when`, the read-back also checks that Things filed the item where
the value puts it: Anytime, Someday, today, this evening or the date. If
it was modified but filed somewhere else, `edit` exits non-zero with `edit
did not apply as sent: …` and says where the item is. `add` and `project
add` check the same, and exit non-zero with `add did not apply as sent: …`
naming the item they found; search for the title before adding it again,
since another add of the same title at the same moment can be the one
found. Under `--json` either error is one object with `"error":
"misfiled"`, the item's `"uuid"` and where it `"landed"`. An English phrase is
not checked. A write sent just before midnight counts as filed for either
day.
An edit with no field flags and no status to change prints the item
without waiting. `--complete` or `--cancel` on an open item waits for the
status, as `things complete` and `things cancel` do.

`--complete` and `--cancel` follow the same rule as `things complete` and
`things cancel`. On an item already in that state, the status is left out
and the rest of the edit is handled as above, with a note. When nothing
else would change the item and there is no `--reveal`, nothing is sent and
the item is printed. Completing a cancelled item, or cancelling a completed
one, refuses the whole edit and sends nothing. With `--duplicate` the status
is sent as asked, since it applies to the copy and the item stays as it is.

Two cases are not read back, and say so instead of printing the item:

- `--no-verify` (or `no_verify = true`): `Sent to Things, not confirmed
  (--no-verify): "Buy oat milk" (…)`.
- `--duplicate`: Things applies the edit to a new copy whose UUID the CLI
  cannot learn, and leaves the original as it was.

Under `--json` both print `{"uuid": …, "title": …, "confirmed": false,
"reason": "no-verify"}` (or `"duplicate"`), so a script can tell an
unconfirmed edit from a confirmed one.

`edit` is for tasks only. A reference that resolves to a project is refused
before anything is written, because `things:///update` cannot address one —
use `things project edit` instead. `project edit` refuses a task the same
way, pointing back at `things edit`.

`things project edit` takes most of the same flags (`--title`, `--notes`,
`--prepend-notes`/`--append-notes`, `--when`, `--deadline`, `--tags`,
`--add-tags`, `--complete`, `--cancel`, `--duplicate`, `--reveal`) plus
`--area`/`--area-id` to move the project. It has no checklist or
heading flags. `--area` takes an area name or UUID, matched as for
`project add`; a UUID goes to Things as `area-id`. An area Things cannot
match, by name or by `--area-id`, leaves the project where it is, and
`project edit` warns. When several areas share the title, it notes which
one Things will use, as `project add` does, unless the project is already
there. A move to the area the project is already in counts as no change.

```
warning: Things has no area called "Nowhere"; the project will stay where it is
warning: Things has no area with id "abc123"; the project will stay where it is
```

## Tags must already exist

Things applies only tags that already exist and drops the rest without
saying so. Every write that carries tags (`add`, `project add`, `edit`,
`project edit`, `import`) checks them against the database first and warns:

```sh
things add "Review the flags" --tags "Work,cifas-auto-reject"
# warning: these tags do not exist in Things and will be ignored: cifas-auto-reject
```

The write still happens. Add `--create-tags` to create the missing tags
first so the write applies them all, or `--strict-tags` to fail and write
nothing instead. The two contradict each other and are rejected together.

## Creating tags

```sh
things tag add focus "deep work"
things tag add Work                 # skipped, it already exists
things tag add focus --json         # {"created": [...], "skipped": [...]}
```

Names that already exist are skipped rather than duplicated, matched
case-insensitively as Things matches them. Creation goes through
AppleScript, so Things3 must be running; the tag list is read back
afterwards to confirm it landed, which `--no-verify` skips.

## Completing and cancelling

```sh
things complete 3
things cancel 3
things complete "Launch v2" --yes    # skip the project confirmation
```

Completing or cancelling a project also completes or cancels every task
in it, so it asks first. A run that cannot prompt — piped stdin, or
`--json` — declines instead of guessing; `--yes` (`-y`) answers the
question up front, which is how project completion works from a script.
`assume_yes = true` in the config file sets it every time, and `--yes`
still decides each run.

Listings number the items closed today alongside the open ones, so a ref can
land on one. Completing an item that is already completed, or cancelling one
already cancelled, sends nothing and exits 0 with a note. Completing a
cancelled item, or cancelling a completed one, is refused and sends nothing.

Both go through AppleScript so Things3 records the change in its
activity log. Task creation (`add`) and edits go through the
`things:///` URL scheme; the CLI never writes to the database directly.

## Logbook and import

```sh
things log                    # move all of Today's completed items into the Logbook
things import < payload.json  # batch create/update via the Things JSON URL scheme
```

`import` payload is the array
[documented by Cultured Code](https://culturedcode.com/things/support/articles/2803573/).

Items with `"operation": "update"` go through the same repeating check as
`edit`: if any of them carries `when`, `deadline`, `completed` or `canceled`
for a repeating task or project, the whole import is refused before anything
is sent, and the error names every offending item. The status fields are
two-way, so `false` is refused as readily as `true`. Update items that set
`completed` or `canceled` are read back from the database afterwards, and any
that Things dropped are reported one per line with a non-zero exit.

A `creation-date` or `completion-date` on a to-do or project the payload
creates or updates must be a date and time with seconds and a UTC offset,
such as `2026-10-05T10:30:00Z` or `2026-10-05T10:30:00+02:00` (`+0200` and
`+02` work too). Things rejects the whole payload over a date on its own, a
time with no seconds or no offset, a lowercase `t` or `z`, or a comma before
the fraction of a second, so `import` refuses it before anything is sent
(tags included) and names each item, with the id of an update item. A
`null` creation-date is no date: Things saves the item as created now.

Both refusals are one: a run names every refused item, whichever the
reason. Under `--json` it is `import refused`, with one entry per item:
`blocked` names every attribute refused on it, and `reason` says why,
`repeating`, `invalid-date`, or both separated by a space.

Every to-do and project the payload creates is read back too, the way
`add` finds its item: a new item of that kind with that title (surrounding
whitespace trimmed) that was not there before the import, filed where the
payload puts it, as Things files it:

- A to-do with a `heading-id` goes under that heading, in its project,
  whatever list it names. Any `heading-id`, even an empty or unknown one,
  makes Things ignore `heading`; an unknown one is otherwise ignored.
- Otherwise a `list-id` wins over `list`. An empty or unknown `list-id`
  puts the to-do in the Inbox.
- A `list-id` or `heading-id` Things has files the to-do there whatever
  state its project is in: a closed or logged project is reopened, and a
  trashed one takes the to-do into the Trash.
- A `list` title goes to the list Things picks for that title, as for
  `add`, under its `heading` when it has one. A title that matches
  nothing, or a uuid given as `list`, puts the to-do in the Inbox. When a
  project created earlier in the same payload has that title, the to-do
  may go to either, so any list with the title counts. A project created
  later in the payload is not there yet.
- A to-do with none of these goes to the Inbox.
- A project goes to the area its `area-id` names, or else its `area`. An
  empty or unknown `area-id`, or an `area` that matches nothing, leaves it
  in no area.

Things does not trim an id, so a `heading-id`, `list-id` or `area-id` with
surrounding space matches nothing, and a `null` one counts as not given.
`import` prints a warning for each item that Things will not file where
the payload asks, as `add` does. A to-do in a project's `items` must be in
that project. To-dos inside a project the payload creates are read back
too. A to-do inside the `items` of
a project the payload updates is checked too: Things drops those without
saying so, and the read-back reports it as `not-found`. Headings and
checklist items are not read back, and nor is an item with no title. `import` prints one line per created item:

```text
Created and confirmed: [0] "Buy oat milk" (8QK2xgV1m3C9p7RfT4hLwe)
Created and confirmed: [1] "Launch" (Hs5TqPz2aN8wYc1Lk3RmVd)
Created and confirmed: [1].attributes.items[0] "Book venue" (Ft7…)
```

Under `--json` it prints an array, one object per created item:
`{"path", "kind": "task"|"project", "title", "uuid", "confirmed": true}`.
When several created items share a kind and title, they are confirmed once
that many new items appear, and paired with the new items in the order
Things saved them. If two of those new items have the same creation time,
the order says nothing, so they are all reported `ambiguous` instead.

An item that is not confirmed has `"confirmed": false` and a `reason`, as
an unconfirmed `add` does, and no `uuid`:

- `no-verify`: `--no-verify` (or `no_verify = true`) skipped the read-back.
- `unreadable`: the database could not be read, so a warning is printed.
- `ambiguous`: more new items with that title appeared than the payload
  created, or they cannot be paired (see above), or a new item fits two
  created items because one of them has a destination that is not checked.
  `candidates` lists their uuids.
- `creation-date`: the payload sets the item's `creation-date`. Things
  saves the item with that date, so the read-back cannot tell it from older
  items and does not look for it. The line says `not checked
  (creation-date set)`.
- `shares-dated-title`: another item in the payload with the same kind and
  title sets a `creation-date` within the last minute or later, and a new
  item filed where this one goes is also filed where that one goes. It
  could be either one, so this item is not confirmed. `candidates` lists
  the new items with that kind and title filed where this item goes. When
  the database cannot be read, any recent dated item with the same kind
  and title counts. The line says `not confirmed (a dated item has the
  same title)`. An older `creation-date` cannot be mistaken for a new item,
  so it does not stop the read-back.
- `not-found`: no new item with that title appeared within the read-back
  wait, or fewer than the payload created, or fewer than the created items
  that could each be filed where they appeared (when one of them has a
  destination that is not checked). `candidates` lists the ones that did
  appear.

The first four exit 0 with the list printed. Any `not-found` or
`shares-dated-title` item makes the import exit non-zero with `import
partially applied`, the same error a dropped status change gives. The error
names those items, and under `--json` they are in `items`. Search for each
of them with `things search` before running the import again with only
those items. Otherwise a retry can create duplicates. The verdict on every
other created item follows under "The other created items", and under `--json` the error's
`created` array carries every created item in the shape above, so the
confirmed ones keep their uuids. The same happens when only a status change
failed. The created items and the status changes share one read-back wait.

## Opening in the app

```sh
things open today              # built-in views
things open inbox
things open <uuid>             # specific task or project
things open "Weekly Review"    # task or project by title
things open --area "Side projects"   # an area (bare titles never resolve areas; names match case-insensitively)
things open --tag urgent             # a tag (names match case-insensitively)
```

## Agent skill

`things skill install <agent>` installs the bundled skill for Claude Code,
Codex or Pi. See [Working with agents](/agents/).

## Shell completions

`things completions <shell>` prints a completion script for `bash`, `zsh`,
or `fish`:

```sh
things completions zsh > ~/.things-completions.zsh   # then source it from ~/.zshrc
```

Homebrew-cask installs wire these up automatically.

## Version

```sh
things version       # or: things --version / things -v
```

Prints the version, commit, and build date.

## Update

```sh
things update            # update to the latest release
things update --dry-run  # check for a release, print the command, and stop
```

Updates `things` the same way it was installed. It looks at where the
running binary lives and how it was built:

- In the Homebrew Caskroom: runs
  `brew upgrade --cask ryanlewis/tap/things`.
- A release binary from the install script or a tarball: re-runs the
  [install script](/install/#one-line-install-script) with `INSTALL_DIR`
  set to the binary's own directory. If that directory isn't writable,
  it stops and prints the command for you to run, since the script asks
  for `sudo`.
- Built by `go install ...@latest`: runs
  `go install github.com/ryanlewis/things-cli/cmd/things@<tag>` for the
  latest release tag, with `GOBIN` set to the binary's own directory.
- A local build (`make install`, `go build`): refuses, because there is
  no release to update it from.

Before updating, it asks GitHub for the latest release, with or without
`--dry-run`. It stops if you already have it, or if yours is newer. Only
`MAJOR.MINOR.PATCH` is compared for "newer", so `1.0.0-rc1` is not newer
than `1.0.0` and updates to it. The install script is then fetched from
that release's tag and told to install that version, and `go install`
is pinned to the same tag. The check uses the GitHub API, with a token
from `GH_TOKEN` or `GITHUB_TOKEN` when one is set (sent to
`api.github.com` only), which avoids the API's rate limit for
unauthenticated calls. If the API call fails, it reads the tag from where
`https://github.com/ryanlewis/things-cli/releases/latest` redirects, and
prints a note on stderr naming the API error, so a bad or expired token
shows up. If
neither works (offline, say), or the latest tag isn't a `vMAJOR.MINOR.PATCH` version it can
compare and pin, the install script and `go install`
methods stop with an error and print the command for you to run
yourself: both pick the latest release on their own and could replace a
newer binary with an older one. Homebrew carries on, since
`brew upgrade` makes its own check. It prints the command before
running it.

## Configuration

A TOML file at `~/.config/things-cli/config.toml` supplies defaults for
the flags above, so you can set them once instead of typing them every
run. Precedence is flag > config file > built-in default.

```sh
things config init     # write a commented template
things config path     # the file in use, and whether it exists
things config show     # the defaults it establishes
```

See [Configuration](/configuration/) for the full key
table, an annotated example file, and what happens when the file is
wrong. The three `config` subcommands keep working against a file the
CLI cannot use — they are how you find out what is wrong with it.

## Caching

`things` caches the last list it printed in
`$HOME/Library/Caches/things-cli/last-list` so that numeric indices
(`things show 3`) work across invocations. Clear it by deleting that
file or by running any plain list command, which overwrites it.

The cache records when the listing ran and which command printed it.
A row number is good for four hours; past that a numeric reference is
refused rather than acted on, because the rows behind it have probably
moved. The error names the listing to re-run:

```console
$ things complete 2
Error: task #2 comes from a stale list cache: the rows were listed over 2 days ago, older than the 4 hours a row number is good for. Re-run `things today` and use the new row number, or pass the task's uuid.
```

Re-running that listing renumbers the rows and clears the refusal. A
UUID is never refused, and neither is a title. A cache file written by
a version before 0.8.0 records no time, so the first numeric reference
after upgrading is refused until you list again. The named listing
carries `--db` when the flag supplied one, and `--config` when the flag
named the config file, so it re-reads the database the rows came from.

The cache also records which database the listing read, by its resolved
path. A numeric reference made against a different database is refused
too, however recent the listing, since its rows describe the other one:

```console
$ things --db ~/backup/main.sqlite today
$ things complete 2
Error: task #2 comes from a listing of a different database: `things --db /Users/me/backup/main.sqlite today` read /Users/me/backup/main.sqlite, and this command reads /Users/me/Library/Group Containers/…/main.sqlite. Re-run the listing against this database and use the new row number, or pass the task's uuid.
```

A relative path, a symlink, or a hard link to the same file, or the same
path typed in a different case, counts as the same
database. A cache file written before the database was recorded backs a
row number only when no `--db` is in play (from the flag or the config
file); with one, the reference is refused until you list again.

A `--json` listing never writes the cache. JSON output carries no row
numbers, so it has nothing to record, and the file is one shared cache
per machine rather than one per shell — writing it from a scripted run
would move the numbers a person is reading from another window.
