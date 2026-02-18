-- Migration: Add is_active column to preserve cleanup history
-- This allows cleanup to mark entries as inactive instead of deleting them,
-- preserving historical data for logging and audit purposes.

-- Add is_active column if it doesn't exist (defaults to TRUE for all existing records)
DO $$ 
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM information_schema.columns 
        WHERE table_name = 'services' AND column_name = 'is_active'
    ) THEN
        ALTER TABLE services ADD COLUMN is_active BOOLEAN NOT NULL DEFAULT TRUE;
    END IF;
END $$;

-- Add index for efficient filtering of active services
CREATE INDEX IF NOT EXISTS idx_services_is_active ON services(is_active);

-- Update any records with very old heartbeats to be marked inactive
-- (Optional: preserves existing cleanup behavior for records that should have been cleaned up)
-- Uncomment the following if you want to mark old entries as inactive on migration:
-- UPDATE services SET is_active = FALSE 
-- WHERE last_heartbeat < NOW() - INTERVAL '60 minutes';
