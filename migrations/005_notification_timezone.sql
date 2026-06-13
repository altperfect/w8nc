ALTER TABLE notification_settings
    ADD COLUMN IF NOT EXISTS timezone TEXT NULL;
