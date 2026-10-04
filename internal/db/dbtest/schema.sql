CREATE TABLE TMTask (
    uuid                            TEXT PRIMARY KEY,
    title                           TEXT,
    notes                           TEXT,
    type                            INTEGER,
    status                          INTEGER,
    stopDate                        REAL,
    creationDate                    REAL,
    userModificationDate            REAL,
    trashed                         INTEGER,
    start                           INTEGER,
    startDate                       INTEGER,
    startBucket                     INTEGER,
    reminderTime                    INTEGER,
    deadline                        INTEGER,
    deadlineSuppressionDate         INTEGER,
    "index"                         INTEGER,
    todayIndex                      INTEGER,
    todayIndexReferenceDate         INTEGER,
    area                            TEXT,
    project                         TEXT,
    heading                         TEXT,
    untrashedLeafActionsCount       INTEGER,
    openUntrashedLeafActionsCount   INTEGER,
    rt1_recurrenceRule              BLOB
);

CREATE TABLE TMArea (
    uuid     TEXT PRIMARY KEY,
    title    TEXT,
    visible  INTEGER,
    "index"  INTEGER
);

CREATE TABLE TMTag (
    uuid     TEXT PRIMARY KEY,
    title    TEXT,
    shortcut TEXT,
    parent   TEXT,
    "index"  INTEGER
);

CREATE TABLE TMTaskTag (
    tasks TEXT NOT NULL,
    tags  TEXT NOT NULL
);

CREATE TABLE TMChecklistItem (
    uuid     TEXT PRIMARY KEY,
    title    TEXT,
    status   INTEGER,
    stopDate REAL,
    "index"  INTEGER,
    task     TEXT
);

-- Things indexes the parent column, and the task queries' checklist counts
-- lean on it.
CREATE INDEX index_TMChecklistItem_task ON TMChecklistItem (task);

CREATE TABLE TMSettings (
    uuid                         TEXT PRIMARY KEY,
    uriSchemeAuthenticationToken TEXT,
    logInterval                  INTEGER,
    manualLogDate                REAL
);
