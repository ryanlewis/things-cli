#!/usr/bin/env python3
"""Synthetic Things3 backend for the README demo recording.

The demo runs the real `things` binary against a throwaway SQLite database so
no personal data is ever on screen. Reads need nothing more than the database,
but writes go through `open things:///...` (URL scheme) and `osascript`
(AppleScript), which would hit the real Things app. env.sh puts this script on
PATH under both of those names, and it applies the write to the demo database
instead. The CLI's own read-back verification then sees the change land, the
same as it would against Things.

Subcommands:
  seed <db> <schema.sql>   create and populate the demo database
  open [-g] <things-url>   handle a things:///add or things:///update URL
  osascript -e <script>    handle the complete/cancel/log/tag scripts
"""

import datetime as dt
import os
import random
import re
import sqlite3
import string
import sys
import time
from urllib.parse import parse_qs, urlsplit

# REAL timestamp columns (creationDate, stopDate, manualLogDate) are Unix seconds.


def things_date(d):
    """model.ThingsDate: year<<16 | month<<12 | day<<7."""
    return (d.year << 16) | (d.month << 12) | (d.day << 7)


def now_unix():
    return float(int(time.time()))


def parse_ymd(s):
    return dt.date.fromisoformat(s)


# --- seed -------------------------------------------------------------------

# Deterministic so a re-record shows the same UUIDs.
_rng = random.Random(50)
_ALPHABET = string.ascii_letters + string.digits


def new_uuid():
    return "".join(_rng.choice(_ALPHABET) for _ in range(22))


def seed(db_path, schema_path):
    today = dt.date.today()
    td = things_date(today)
    created = now_unix() - 3 * 86400

    with open(schema_path, encoding="utf-8") as f:
        schema = f.read()
    conn = sqlite3.connect(db_path)
    conn.executescript(schema)
    cur = conn.cursor()

    cur.execute(
        "INSERT INTO TMSettings (uuid, uriSchemeAuthenticationToken, manualLogDate) VALUES (?, ?, 0)",
        (new_uuid(), "demo-token"),
    )

    areas = {"Work": new_uuid(), "Home": new_uuid()}
    for i, (title, uuid) in enumerate(areas.items(), start=1):
        cur.execute(
            'INSERT INTO TMArea (uuid, title, visible, "index") VALUES (?, ?, 1, ?)',
            (uuid, title, i),
        )

    tags = {}
    for i, name in enumerate(["release", "errand", "urgent", "home", "reading"], start=1):
        tags[name] = new_uuid()
        cur.execute('INSERT INTO TMTag (uuid, title, "index") VALUES (?, ?, ?)', (tags[name], name, i))

    def task(title, *, type=0, start=1, start_date=None, deadline=None, project=None,
             area=None, index=0, today_index=0, tags_=(), notes=None, status=0):
        uuid = new_uuid()
        cur.execute(
            """INSERT INTO TMTask
               (uuid, title, notes, type, status, creationDate, trashed, start, startBucket,
                startDate, todayIndexReferenceDate, deadline, project, area, "index", todayIndex)
               VALUES (?, ?, ?, ?, ?, ?, 0, ?, 0, ?, ?, ?, ?, ?, ?, ?)""",
            (uuid, title, notes, type, status, created, start, start_date,
             td if start_date == td else None, deadline, project, area, index, today_index),
        )
        for t in tags_:
            cur.execute("INSERT INTO TMTaskTag (tasks, tags) VALUES (?, ?)", (uuid, tags[t]))
        return uuid

    # Today: two loose to-dos, then a project's to-dos.
    task("Renew passport", start_date=td, deadline=things_date(today + dt.timedelta(days=12)),
         index=1, today_index=1)
    task("Call the plumber about the boiler", start_date=td, tags_=("home",), index=2, today_index=2)

    launch = task("Launch v2", type=1, start_date=td, area=areas["Work"], index=3,
                  notes="Ship the redesign and the new billing flow.")
    task("Cut the release candidate", start_date=td, deadline=things_date(today + dt.timedelta(days=4)),
         project=launch, tags_=("release",), index=1, today_index=3)
    task("Write the release notes", start_date=td, project=launch, index=2, today_index=4,
         notes="Cover the redesign, billing, and the migration steps.")
    task("Update the changelog", start_date=td, project=launch, index=3, today_index=5)

    # Elsewhere, so the database looks lived-in for inbox/upcoming/search.
    task("Pick a new standing desk", start=0, index=4)
    task("Book the car in for a service", start=2, start_date=things_date(today + dt.timedelta(days=9)),
         area=areas["Home"], index=5)
    task("Read the release engineering book", start=2, tags_=("reading",), index=6)
    task("Write the v1 release notes", status=3, project=launch, index=0,
         start_date=things_date(today - dt.timedelta(days=30)))
    cur.execute("UPDATE TMTask SET stopDate = ? WHERE title = 'Write the v1 release notes'",
                (now_unix() - 25 * 86400,))

    conn.commit()
    conn.close()


# --- write shims ------------------------------------------------------------

def connect():
    path = os.environ.get("THINGS_DEMO_DB")
    if not path:
        sys.exit("THINGS_DEMO_DB is not set")
    return sqlite3.connect(path)


def tag_ids(cur, csv):
    ids = []
    for name in (n.strip() for n in csv.split(",")):
        if not name:
            continue
        row = cur.execute("SELECT uuid FROM TMTag WHERE title = ?", (name,)).fetchone()
        if row:  # Things silently drops unknown tags; so do we
            ids.append(row[0])
    return ids


def apply_when(cur, uuid, when):
    today = dt.date.today()
    when = when.lower()
    if when in ("today", "evening"):
        start, start_date = 1, things_date(today)
    elif when == "tomorrow":
        start, start_date = 2, things_date(today + dt.timedelta(days=1))
    elif when == "anytime":
        start, start_date = 1, None
    elif when == "someday":
        start, start_date = 2, None
    else:
        d = parse_ymd(when.split("@")[0])
        start, start_date = (1, things_date(d)) if d <= today else (2, things_date(d))
    cur.execute(
        "UPDATE TMTask SET start = ?, startBucket = 0, startDate = ?, todayIndexReferenceDate = ? WHERE uuid = ?",
        (start, start_date, start_date if start == 1 and start_date else None, uuid),
    )


def set_status(cur, uuid, status):
    cur.execute("UPDATE TMTask SET status = ?, stopDate = ? WHERE uuid = ?",
                (status, now_unix() if status else None, uuid))


def handle_open(args):
    url = next((a for a in args if a.startswith("things://")), None)
    if url is None:
        return  # `open` of anything else: not ours to fake
    parts = urlsplit(url)
    command = parts.path.lstrip("/")
    q = {k: v[0] for k, v in parse_qs(parts.query, keep_blank_values=True).items()}
    if command in ("show", "search"):
        return  # reveal-only, nothing to record

    conn = connect()
    cur = conn.cursor()
    if command in ("add", "add-project"):
        uuid = new_uuid()
        is_project = command == "add-project"
        next_index = cur.execute('SELECT COALESCE(MAX("index"), 0) + 1 FROM TMTask').fetchone()[0]
        next_today = cur.execute("SELECT COALESCE(MAX(todayIndex), 0) + 1 FROM TMTask").fetchone()[0]
        project = area = None
        if q.get("list"):
            row = cur.execute("SELECT uuid, type FROM TMTask WHERE title = ? AND type = 1", (q["list"],)).fetchone()
            if row:
                project = row[0]
            else:
                row = cur.execute("SELECT uuid FROM TMArea WHERE title = ?", (q["list"],)).fetchone()
                area = row[0] if row else None
        if q.get("area"):
            row = cur.execute("SELECT uuid FROM TMArea WHERE title = ?", (q["area"],)).fetchone()
            area = row[0] if row else None
        cur.execute(
            """INSERT INTO TMTask (uuid, title, notes, type, status, creationDate, trashed, start,
               startBucket, startDate, deadline, project, area, "index", todayIndex)
               VALUES (?, ?, ?, ?, 0, ?, 0, 0, 0, NULL, ?, ?, ?, ?, ?)""",
            (uuid, q.get("title", ""), q.get("notes") or None, 1 if is_project else 0, now_unix(),
             things_date(parse_ymd(q["deadline"])) if q.get("deadline") else None,
             project, area, next_index, next_today),
        )
        if q.get("when"):
            apply_when(cur, uuid, q["when"])
        for t in tag_ids(cur, q.get("tags", "")):
            cur.execute("INSERT INTO TMTaskTag (tasks, tags) VALUES (?, ?)", (uuid, t))
        for i, item in enumerate(filter(None, q.get("checklist-items", "").split("\n"))):
            cur.execute('INSERT INTO TMChecklistItem (uuid, title, status, "index", task) VALUES (?, ?, 0, ?, ?)',
                        (new_uuid(), item, i, uuid))
        if q.get("completed") == "true":
            set_status(cur, uuid, 3)
    elif command in ("update", "update-project"):
        uuid = q["id"]
        for key, col in (("title", "title"), ("notes", "notes")):
            if key in q:
                cur.execute(f"UPDATE TMTask SET {col} = ? WHERE uuid = ?", (q[key], uuid))
        if "append-notes" in q:
            cur.execute("UPDATE TMTask SET notes = COALESCE(notes, '') || ? WHERE uuid = ?", (q["append-notes"], uuid))
        if "deadline" in q:
            deadline = things_date(parse_ymd(q["deadline"])) if q["deadline"] else None
            cur.execute("UPDATE TMTask SET deadline = ? WHERE uuid = ?", (deadline, uuid))
        if "when" in q and q["when"]:
            apply_when(cur, uuid, q["when"])
        if "tags" in q:
            cur.execute("DELETE FROM TMTaskTag WHERE tasks = ?", (uuid,))
            for t in tag_ids(cur, q["tags"]):
                cur.execute("INSERT INTO TMTaskTag (tasks, tags) VALUES (?, ?)", (uuid, t))
        if "add-tags" in q:
            for t in tag_ids(cur, q["add-tags"]):
                cur.execute("INSERT INTO TMTaskTag (tasks, tags) SELECT ?, ? WHERE NOT EXISTS "
                            "(SELECT 1 FROM TMTaskTag WHERE tasks = ? AND tags = ?)", (uuid, t, uuid, t))
        if q.get("completed") == "true":
            set_status(cur, uuid, 3)
        if q.get("canceled") == "true":
            set_status(cur, uuid, 2)
    else:
        sys.exit(f"demo shim: unhandled things:///{command}")
    conn.commit()
    conn.close()


def handle_osascript(args):
    script = args[args.index("-e") + 1] if "-e" in args else ""
    conn = connect()
    cur = conn.cursor()
    m = re.search(r'(?:to do|project) id "([^"]+)"', script)
    status = re.search(r"set status of \w+ to (completed|canceled)", script)
    tag = re.search(r'make new tag with properties \{name:"((?:[^"\\]|\\.)*)"\}', script)
    if m and status:
        set_status(cur, m.group(1), 3 if status.group(1) == "completed" else 2)
    elif tag:
        name = tag.group(1).replace('\\"', '"').replace("\\\\", "\\")
        nxt = cur.execute('SELECT COALESCE(MAX("index"), 0) + 1 FROM TMTag').fetchone()[0]
        cur.execute('INSERT INTO TMTag (uuid, title, "index") VALUES (?, ?, ?)', (new_uuid(), name, nxt))
    elif "log completed now" in script:
        cur.execute("UPDATE TMSettings SET manualLogDate = ?", (now_unix(),))
    else:
        sys.exit("demo shim: unhandled AppleScript:\n" + script)
    conn.commit()
    conn.close()


def main(argv):
    if not argv:
        sys.exit(__doc__)
    cmd, args = argv[0], argv[1:]
    if cmd == "seed":
        seed(*args)
    elif cmd == "open":
        handle_open(args)
    elif cmd == "osascript":
        handle_osascript(args)
    else:
        sys.exit(f"unknown subcommand {cmd!r}")


if __name__ == "__main__":
    main(sys.argv[1:])
