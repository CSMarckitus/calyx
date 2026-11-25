-- The free-text device name becomes the device identity of the core: one name of its list
-- (DeviceIdentity without the prefix DEVICE_IDENTITY_), or NULL. The core never received the old
-- free text, so a value outside the list has no meaning and becomes NULL.
ALTER TABLE instances RENAME COLUMN device_name TO device_identity;
UPDATE instances SET device_identity = NULL
WHERE device_identity NOT IN ('CHROME_WINDOWS', 'EDGE_WINDOWS', 'FIREFOX_WINDOWS', 'FIREFOX_LINUX', 'SAFARI_MAC_OS');

-- "language-COUNTRY", for example es-ES. The core sends it to WhatsApp at each connection.
ALTER TABLE instances ADD COLUMN locale text NULL;
