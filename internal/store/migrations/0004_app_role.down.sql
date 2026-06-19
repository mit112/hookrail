-- Do NOT DROP ROLE (cluster-wide; would poison a shared container and can fail on dependent grants).
-- Only REVOKE the grants; leave the role.
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE SELECT, INSERT, UPDATE, DELETE ON TABLES FROM hookrail_app;
ALTER DEFAULT PRIVILEGES IN SCHEMA public REVOKE USAGE, SELECT ON SEQUENCES FROM hookrail_app;
REVOKE SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public FROM hookrail_app;
REVOKE USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public FROM hookrail_app;
REVOKE USAGE ON SCHEMA public FROM hookrail_app;
