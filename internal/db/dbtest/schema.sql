CREATE TABLE TMTask (
    uuid                            TEXT PRIMARY KEY,
    title                           TEXT,
    notes                           TEXT,
    type                            INTEGER,
    status                          INTEGER,
    stopDate                        REAL,
    creationDate                    REAL,
    trashed                         INTEGER,
    start                           INTEGER,
    startDate                       INTEGER,
    startBucket                     INTEGER,
    deadline                        INTEGER,
    "index"                         INTEGER,
    todayIndex                      INTEGER,
    todayIndexReferenceDate         INTEGER,
    area                            TEXT,
    project                         TEXT,
    heading                         TEXT,
    untrashedLeafActionsCount       INTEGER,
    openUntrashedLeafActionsCount   INTEGER
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

CREATE TABLE TMSettings (
    uuid                         TEXT PRIMARY KEY,
    uriSchemeAuthenticationToken TEXT
);
