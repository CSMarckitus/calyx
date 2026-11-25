-- Device identity and locale of a WhatsApp Web instance. NULL means no value: a Meta Cloud
-- instance, or a WhatsApp Web instance paired before these columns existed.

-- The DeviceIdentity enum name, for example DEVICE_IDENTITY_FIREFOX_LINUX. It changes at each pairing.
ALTER TABLE instances ADD COLUMN device_identity TEXT;

-- "language-COUNTRY", for example es-ES. Sent to WhatsApp at each connection.
ALTER TABLE instances ADD COLUMN locale TEXT;
