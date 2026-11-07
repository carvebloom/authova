-- Authova PostgreSQL 17+ RLS design draft; apply after schema-v1.sql in a
-- disposable database for review. Production roles/users and GRANTs require
-- audited migrations. authova_app and authova_router must never own tables,
-- be superusers, have BYPASSRLS, or be able to SET ROLE authova_platform.

CREATE ROLE authova_app NOLOGIN;
CREATE ROLE authova_router NOLOGIN;
CREATE ROLE authova_platform NOLOGIN;

ALTER TABLE public.apps ENABLE ROW LEVEL SECURITY;
ALTER TABLE public.apps FORCE ROW LEVEL SECURITY;
CREATE POLICY scoped_app ON public.apps FOR SELECT TO authova_app
    USING (id = NULLIF(current_setting('authova.app_id', true),'')::uuid);
CREATE POLICY route_app ON public.apps FOR SELECT TO authova_router
    USING (true);
CREATE POLICY platform_app ON public.apps FOR ALL TO authova_platform
    USING (true) WITH CHECK (true);
GRANT SELECT (id,app_key,issuer,status,config_version) ON public.apps TO authova_app;
GRANT SELECT (id,app_key,slug,issuer,status) ON public.apps TO authova_router;
GRANT SELECT,INSERT,UPDATE,DELETE ON public.apps TO authova_platform;

DO $rls$
DECLARE
    table_name text;
BEGIN
    FOREACH table_name IN ARRAY ARRAY[
        'app_signing_keys',
        'clients', 'client_redirect_uris', 'client_secrets',
        'users', 'user_identifiers', 'identifier_lookup_aliases',
        'password_credentials', 'provider_connections',
        'external_identities', 'external_identity_lookup_aliases',
        'auth_challenges', 'auth_flows', 'sessions', 'auth_codes',
        'oauth_consents', 'refresh_families', 'refresh_tokens',
        'app_admin_grants',
        'audit_events', 'outbox_events', 'idempotency_records'
    ] LOOP
        EXECUTE format('ALTER TABLE public.%I ENABLE ROW LEVEL SECURITY',table_name);
        EXECUTE format('ALTER TABLE public.%I FORCE ROW LEVEL SECURITY',table_name);
        EXECUTE format(
            'CREATE POLICY app_scope ON public.%I FOR ALL TO authova_app '
            || 'USING (app_id = NULLIF(current_setting(''authova.app_id'',true),'''')::uuid) '
            || 'WITH CHECK (app_id = NULLIF(current_setting(''authova.app_id'',true),'''')::uuid)',
            table_name
        );
        EXECUTE format('GRANT SELECT,INSERT,UPDATE,DELETE ON public.%I TO authova_app',table_name);
    END LOOP;
END
$rls$;

-- Only server-owned active Console Clients can be discovered before login.
CREATE POLICY route_system_client ON public.clients FOR SELECT TO authova_router
    USING (is_system AND status='active');
GRANT SELECT (app_id,client_id,is_system,status) ON public.clients TO authova_router;

-- Platform writes/readbacks are limited to platform-owned rows in shared
-- event/idempotency tables. Tenant rows still require an App-scoped transaction.
CREATE POLICY platform_audit ON public.audit_events FOR ALL TO authova_platform
    USING (app_id IS NULL) WITH CHECK (app_id IS NULL);
CREATE POLICY platform_outbox ON public.outbox_events FOR ALL TO authova_platform
    USING (app_id IS NULL) WITH CHECK (app_id IS NULL);
CREATE POLICY platform_idempotency ON public.idempotency_records FOR ALL TO authova_platform
    USING (scope_kind='platform' AND app_id IS NULL)
    WITH CHECK (scope_kind='platform' AND app_id IS NULL);
GRANT SELECT,INSERT,UPDATE,DELETE ON public.audit_events,public.outbox_events,public.idempotency_records TO authova_platform;

GRANT SELECT,INSERT,UPDATE,DELETE ON
    public.platform_operators,
    public.platform_bootstrap_guard,
    public.platform_activation_challenges,
    public.platform_operator_roles,
    public.platform_sessions,
    public.platform_signing_keys,
    public.platform_clients,
    public.platform_auth_flows,
    public.platform_auth_codes
TO authova_platform;
GRANT SELECT (client_id,status) ON public.platform_clients TO authova_router;

-- App runtime transaction pattern:
-- BEGIN;
-- SET LOCAL authova.app_id = '11111111-1111-4111-8111-111111111111';
-- ... all App-owned reads and writes ...
-- COMMIT;
-- The pool never executes App queries outside this transaction wrapper.
