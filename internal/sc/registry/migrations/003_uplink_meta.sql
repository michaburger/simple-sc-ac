-- Flags of an uplink that SCACI passes on (dlOpen, responseExp, dlAck), as JSON.
ALTER TABLE uplink ADD COLUMN meta TEXT NOT NULL DEFAULT '{}';
