CREATE TABLE IF NOT EXISTS services (
	id SERIAL PRIMARY KEY,
	task TEXT NOT NULL,
	address TEXT NOT NULL,
	last_heartbeat TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	query_count BIGINT NOT NULL DEFAULT 0,
	created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	updated_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW(),
	UNIQUE(task, address)
);

CREATE INDEX IF NOT EXISTS idx_services_task ON services(task);
CREATE INDEX IF NOT EXISTS idx_services_last_heartbeat ON services(last_heartbeat);
