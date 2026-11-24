-- Application Center store. Identifiers as IDT text, keys as upper case hex text, times
-- as integer nanoseconds UTC.

CREATE TABLE end_point (
    ep_eui        TEXT PRIMARY KEY,
    class         TEXT NOT NULL DEFAULT 'a' CHECK (class IN ('z', 'a', 'b', 'c')),
    nwk_key       TEXT NOT NULL,
    sh_addr       INTEGER,
    pre_attach    INTEGER NOT NULL DEFAULT 0,
    attach_cnt    INTEGER NOT NULL DEFAULT 0,
    packet_cnt    INTEGER NOT NULL DEFAULT 0,
    dual_chan     INTEGER NOT NULL DEFAULT 0,
    repetition    INTEGER NOT NULL DEFAULT 0,
    wide_carr_off INTEGER NOT NULL DEFAULT 0,
    long_blk_dist INTEGER NOT NULL DEFAULT 0,
    name          TEXT NOT NULL DEFAULT '',
    created_ns    INTEGER NOT NULL DEFAULT 0
);

-- Registration of an end-point at one Service Center.
CREATE TABLE registration (
    ep_eui   TEXT NOT NULL,
    sc_name  TEXT NOT NULL,
    state    TEXT NOT NULL CHECK (state IN ('pending', 'registered', 'failed')),
    error    TEXT NOT NULL DEFAULT '',
    updated_ns INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ep_eui, sc_name)
);

-- Last known state of an end-point, from end-point status and uplinks.
CREATE TABLE ep_state (
    ep_eui          TEXT PRIMARY KEY,
    status          TEXT NOT NULL DEFAULT '',
    last_seen_ns    INTEGER NOT NULL DEFAULT 0,
    last_packet_cnt INTEGER NOT NULL DEFAULT -1,
    last_sc         TEXT NOT NULL DEFAULT '',
    last_bs         TEXT NOT NULL DEFAULT ''
);

CREATE TABLE uplink (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    sc_name     TEXT NOT NULL,
    ep_eui      TEXT NOT NULL,
    packet_cnt  INTEGER NOT NULL,
    rx_ns       INTEGER NOT NULL,
    format      INTEGER NOT NULL DEFAULT 0,
    payload     TEXT NOT NULL,
    receptions  TEXT NOT NULL,
    received_ns INTEGER NOT NULL
);
CREATE INDEX uplink_ep ON uplink (ep_eui, packet_cnt);
