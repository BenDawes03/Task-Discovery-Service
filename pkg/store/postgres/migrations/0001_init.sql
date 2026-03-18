CREATE TABLE IF NOT EXISTS services (
	id SERIAL PRIMARY KEY,
	task TEXT NOT NULL,
	address TEXT NOT NULL,
	host INET,
	port INTEGER,
	last_heartbeat TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	query_count BIGINT NOT NULL DEFAULT 0,
	is_active BOOLEAN NOT NULL DEFAULT TRUE,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	UNIQUE(task, address)
);

-- If the table already exists from an older schema, ensure newer columns exist
-- before creating indexes that reference them.
ALTER TABLE services ADD COLUMN IF NOT EXISTS query_count BIGINT NOT NULL DEFAULT 0;
ALTER TABLE services ADD COLUMN IF NOT EXISTS is_active BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE services ADD COLUMN IF NOT EXISTS created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();
ALTER TABLE services ADD COLUMN IF NOT EXISTS updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW();
ALTER TABLE services ADD COLUMN IF NOT EXISTS host INET;
ALTER TABLE services ADD COLUMN IF NOT EXISTS port INTEGER;

-- Ensure ON CONFLICT (task, address) works even if the original UNIQUE constraint
-- wasn't present in an older schema.
CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_address_unique ON services(task, address);
CREATE UNIQUE INDEX IF NOT EXISTS idx_services_task_host_port_unique ON services(task, host, port);

CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);
CREATE INDEX IF NOT EXISTS idx_services_task_host_port ON services(task, host, port);
CREATE INDEX IF NOT EXISTS idx_services_last_heartbeat ON services(last_heartbeat);
CREATE INDEX IF NOT EXISTS idx_services_is_active ON services(is_active);
