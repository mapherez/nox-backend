-- Additive authorization metadata. Existing account/key values are unchanged.
CREATE TABLE suite_user_authority (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 revision TEXT NOT NULL
);
CREATE TABLE suite_personal_keys (
 user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
 key_id TEXT NOT NULL UNIQUE,
 key_hash TEXT NOT NULL UNIQUE
);
INSERT INTO suite_user_authority SELECT id,'authorization_'||lower(hex(randomblob(16))) FROM users;
INSERT INTO suite_personal_keys SELECT user_id,'personal_key_'||lower(hex(randomblob(16))),key_hash FROM api_keys;
CREATE TRIGGER suite_user_created AFTER INSERT ON users BEGIN
 INSERT INTO suite_user_authority VALUES(NEW.id,'authorization_'||lower(hex(randomblob(16))));
END;
CREATE TRIGGER suite_user_authority_changed AFTER UPDATE OF role,status ON users
 WHEN OLD.role<>NEW.role OR OLD.status<>NEW.status BEGIN
 UPDATE suite_user_authority SET revision='authorization_'||lower(hex(randomblob(16))) WHERE user_id=NEW.id;
 INSERT INTO suite_trust_events(event_id,type,event_json,occurred_at)
 VALUES('event_'||lower(hex(randomblob(16))),'authorization.invalidated',
 json_object('eventId','pending','type','authorization.invalidated','occurredAt',strftime('%Y-%m-%dT%H:%M:%fZ','now'),'data',json_object('userId',NEW.id)),
 CAST((julianday('now')-2440587.5)*86400000 AS INTEGER));
END;
CREATE TRIGGER suite_user_removed BEFORE DELETE ON users BEGIN
 INSERT INTO suite_trust_events(event_id,type,event_json,occurred_at)
 VALUES('event_'||lower(hex(randomblob(16))),'authorization.invalidated',
 json_object('eventId','pending','type','authorization.invalidated','occurredAt',strftime('%Y-%m-%dT%H:%M:%fZ','now'),'data',json_object('userId',OLD.id)),
 CAST((julianday('now')-2440587.5)*86400000 AS INTEGER));
END;
CREATE TRIGGER suite_personal_key_created AFTER INSERT ON api_keys BEGIN
 INSERT INTO suite_personal_keys VALUES(NEW.user_id,'personal_key_'||lower(hex(randomblob(16))),NEW.key_hash);
END;
CREATE TRIGGER suite_personal_key_rotated BEFORE UPDATE OF key_hash,key_value ON api_keys
 WHEN OLD.key_hash<>NEW.key_hash OR OLD.key_value<>NEW.key_value BEGIN
 INSERT INTO suite_trust_events(event_id,type,event_json,occurred_at)
 SELECT 'event_'||lower(hex(randomblob(16))),'authorization.invalidated',
 json_object('eventId','pending','type','authorization.invalidated','occurredAt',strftime('%Y-%m-%dT%H:%M:%fZ','now'),'data',json_object('userId',OLD.user_id,'keyId',key_id)),
 CAST((julianday('now')-2440587.5)*86400000 AS INTEGER) FROM suite_personal_keys WHERE user_id=OLD.user_id;
 UPDATE suite_personal_keys SET key_id='personal_key_'||lower(hex(randomblob(16))),key_hash=NEW.key_hash WHERE user_id=NEW.user_id;
 UPDATE suite_user_authority SET revision='authorization_'||lower(hex(randomblob(16))) WHERE user_id=NEW.user_id;
END;
CREATE TRIGGER suite_personal_key_removed BEFORE DELETE ON api_keys BEGIN
 INSERT INTO suite_trust_events(event_id,type,event_json,occurred_at)
 SELECT 'event_'||lower(hex(randomblob(16))),'authorization.invalidated',
 json_object('eventId','pending','type','authorization.invalidated','occurredAt',strftime('%Y-%m-%dT%H:%M:%fZ','now'),'data',json_object('userId',OLD.user_id,'keyId',key_id)),
 CAST((julianday('now')-2440587.5)*86400000 AS INTEGER) FROM suite_personal_keys WHERE user_id=OLD.user_id;
 DELETE FROM suite_personal_keys WHERE user_id=OLD.user_id;
END;
-- Finalize the event envelope from the same opaque ID used by its durable row.
CREATE TRIGGER suite_authorization_event_envelope AFTER INSERT ON suite_trust_events
 WHEN NEW.type='authorization.invalidated' AND json_extract(NEW.event_json,'$.eventId')='pending' BEGIN
 UPDATE suite_trust_events SET event_json=json_set(event_json,'$.eventId',NEW.event_id) WHERE sequence=NEW.sequence;
END;

-- Accepted membership survives an incompatible update; issuance is blocked until
-- the current association/declaration is explicitly verified again.
CREATE TABLE suite_product_admission (
 product_id TEXT PRIMARY KEY REFERENCES suite_products(product_id) ON DELETE CASCADE,
 revision TEXT NOT NULL,
 association_id TEXT NOT NULL,
 blocked INTEGER NOT NULL CHECK(blocked IN (0,1))
);
