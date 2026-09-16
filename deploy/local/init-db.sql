-- APIGX 本地开发库初始化（幂等，可重复执行）
-- 由 tools/dev/init-db.ps1 调用，变量通过 psql -v 传入：
--   app_user / app_password / db_name
--
-- 说明：应用连接使用普通登录角色，不使用超级用户（计划书 §10.3 凭据边界）。

\set ON_ERROR_STOP on

-- 1) 应用角色
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'app_user', :'app_password')
WHERE NOT EXISTS (SELECT FROM pg_roles WHERE rolname = :'app_user')\gexec

-- 已存在时同步密码，保证 .env 与实例一致
SELECT format('ALTER ROLE %I WITH LOGIN PASSWORD %L', :'app_user', :'app_password')\gexec

-- 2) 应用数据库
SELECT format('CREATE DATABASE %I OWNER %I', :'db_name', :'app_user')
WHERE NOT EXISTS (SELECT FROM pg_database WHERE datname = :'db_name')\gexec

-- 3) 新库内的 schema 权限
\connect :db_name
SELECT format('GRANT ALL ON SCHEMA public TO %I', :'app_user')\gexec
SELECT format('ALTER SCHEMA public OWNER TO %I', :'app_user')\gexec

\echo '--- apigx 本地数据库就绪 ---'
