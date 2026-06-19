-- Slice E: least-privilege runtime role. NO password here (assigned at deploy time from a k8s Secret via
-- an owner-run ALTER ROLE). The bootstrap owner `hookrail` remains the migrator (this runs as owner).
DO $$ BEGIN
  IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname='hookrail_app') THEN
    CREATE ROLE hookrail_app NOLOGIN;
  END IF;
END $$;
GRANT USAGE ON SCHEMA public TO hookrail_app;
GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO hookrail_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO hookrail_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO hookrail_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public GRANT USAGE, SELECT ON SEQUENCES TO hookrail_app;
