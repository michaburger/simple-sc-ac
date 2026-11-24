-- Service Center registry. Identifiers as IDT text, keys as upper case hex text, times as
-- integer nanoseconds UTC.

CREATE TABLE base_station (
    bs_eui           TEXT PRIMARY KEY,
    name             TEXT NOT NULL DEFAULT '',
    state            TEXT NOT NULL CHECK (state IN ('pending', 'enabled', 'disabled')),
    cert_fingerprint TEXT NOT NULL DEFAULT '',
    version          TEXT NOT NULL DEFAULT '',
    class            TEXT NOT NULL DEFAULT '',
    bidi             INTEGER NOT NULL DEFAULT 0,
    vendor           TEXT NOT NULL DEFAULT '',
    model            TEXT NOT NULL DEFAULT '',
    sw_version       TEXT NOT NULL DEFAULT '',
    connected        INTEGER NOT NULL DEFAULT 0,
    last_seen_ns     INTEGER NOT NULL DEFAULT 0,
    last_status      TEXT NOT NULL DEFAULT '{}'
);

CREATE TABLE end_point (
    ep_eui          TEXT PRIMARY KEY,
    owner_ac        TEXT NOT NULL DEFAULT '',
    class           TEXT NOT NULL DEFAULT 'a' CHECK (class IN ('z', 'a', 'b', 'c')),
    nwk_key         TEXT NOT NULL,
    sh_addr         INTEGER,                      -- NULL until assigned
    pre_attach      INTEGER NOT NULL DEFAULT 0,
    attach_cnt      INTEGER NOT NULL DEFAULT -1,  -- last accepted attach counter
    last_packet_cnt INTEGER NOT NULL DEFAULT -1,
    state           TEXT NOT NULL DEFAULT 'registered' CHECK (state IN ('registered', 'attached', 'detached')),
    nwk_sn_key      TEXT NOT NULL DEFAULT '',
    dual_chan       INTEGER NOT NULL DEFAULT 0,
    repetition      INTEGER NOT NULL DEFAULT 0,
    wide_carr_off   INTEGER NOT NULL DEFAULT 0,
    long_blk_dist   INTEGER NOT NULL DEFAULT 0,
    attached_via    TEXT NOT NULL DEFAULT '',
    updated_ns      INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE ep_reception (
    ep_eui    TEXT NOT NULL,
    bs_eui    TEXT NOT NULL,
    rx_ns     INTEGER NOT NULL,
    snr       REAL,
    rssi      REAL,
    PRIMARY KEY (ep_eui, bs_eui)
);

-- Deduplicated uplinks. Kept for the Application Center (buffer, G-33) and for the
-- admin interface. receptions is a JSON array with one entry per base station.
CREATE TABLE uplink (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    ep_eui      TEXT NOT NULL,
    packet_cnt  INTEGER NOT NULL,
    rx_ns       INTEGER NOT NULL,
    format      INTEGER NOT NULL DEFAULT 0,
    payload     TEXT NOT NULL,
    receptions  TEXT NOT NULL,
    delivered   INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX uplink_ep ON uplink (ep_eui, packet_cnt);

CREATE TABLE usage_counter (
    ac_eui  TEXT NOT NULL,
    day     TEXT NOT NULL,
    name    TEXT NOT NULL,
    value   INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (ac_eui, day, name)
);
