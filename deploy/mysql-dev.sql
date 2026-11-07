-- Public development fixture passwords. Runtime has no DDL privilege.
REVOKE ALL PRIVILEGES, GRANT OPTION FROM 'authova_runtime'@'%';
GRANT SELECT, INSERT, UPDATE, DELETE ON authova.* TO 'authova_runtime'@'%';
