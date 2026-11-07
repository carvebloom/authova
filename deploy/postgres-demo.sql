-- Local Demo has separate facts from conformance and capacity tests.
SELECT 'CREATE DATABASE authova_demo OWNER authova_migrator'
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = 'authova_demo') \gexec
\connect authova_demo
GRANT CONNECT ON DATABASE authova_demo TO authova_runtime;
GRANT USAGE ON SCHEMA public TO authova_runtime;
ALTER DEFAULT PRIVILEGES FOR ROLE authova_migrator IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO authova_runtime;
