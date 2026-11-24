-- Application Centers that may connect over SCACI, bound to their certificate.
CREATE TABLE application_center (
    ac_eui           TEXT PRIMARY KEY,
    name             TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL CHECK (state IN ('enabled', 'disabled')),
    cert_fingerprint TEXT NOT NULL,
    version          TEXT NOT NULL DEFAULT '',
    connected        INTEGER NOT NULL DEFAULT 0,
    last_seen_ns     INTEGER NOT NULL DEFAULT 0
);

-- One row per end-point and day with at least one uplink (usage counter).
CREATE TABLE ep_active (
    day     TEXT NOT NULL,
    ac_eui  TEXT NOT NULL,
    ep_eui  TEXT NOT NULL,
    PRIMARY KEY (day, ac_eui, ep_eui)
);
