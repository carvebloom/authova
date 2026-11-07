-- Public development fixture passwords. Never use this file for production.
CREATE ROLE authova_runtime LOGIN PASSWORD 'authova-local-runtime-only' NOSUPERUSER NOCREATEDB NOCREATEROLE NOINHERIT NOBYPASSRLS;
GRANT CONNECT ON DATABASE authova TO authova_runtime;
GRANT USAGE ON SCHEMA public TO authova_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE authova_migrator IN SCHEMA public GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO authova_runtime;
