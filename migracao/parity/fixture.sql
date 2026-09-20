-- Somente no cluster descartável exclusivo da matriz. Falha se já inicializado.
CREATE SCHEMA acme;
CREATE SCHEMA beta;
CREATE TABLE acme.probe (id serial primary key, marker text not null);
CREATE TABLE beta.probe (id serial primary key, marker text not null);
INSERT INTO acme.probe (marker) VALUES ('acme-secret');
INSERT INTO beta.probe (marker) VALUES ('beta-secret');
CREATE ROLE parity_user LOGIN PASSWORD 'parity_test_only' NOSUPERUSER NOBYPASSRLS;
GRANT USAGE ON SCHEMA acme TO parity_user;
CREATE TABLE acme.rls_probe (id serial PRIMARY KEY, owner_id text NOT NULL, min_role_read int NOT NULL DEFAULT 1);
ALTER TABLE acme.rls_probe ENABLE ROW LEVEL SECURITY;
ALTER TABLE acme.rls_probe FORCE ROW LEVEL SECURITY;
CREATE POLICY sc_rls_owner ON acme.rls_probe USING (owner_id = current_setting('app.current_user_id', true));
CREATE POLICY sc_rls_elevated ON acme.rls_probe USING (current_setting('app.current_user_role', true)::int <= min_role_read);
GRANT SELECT, INSERT, UPDATE, DELETE ON acme.rls_probe TO parity_user;
INSERT INTO acme.rls_probe (owner_id) VALUES ('user-1'), ('user-2');
