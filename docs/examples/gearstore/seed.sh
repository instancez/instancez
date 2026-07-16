#!/bin/sh
# Seed the gearstore demo once instancez is up. This sidecar shows the two
# data-bootstrap paths instancez supports now that YAML seeding is gone:
#
#   - the admin API (POST /auth/v1/admin/users) for the demo auth user, which
#     handles password hashing and the auth.users internals for us, and
#   - plain SQL (seed.sql via psql) for the catalog rows.
#
# It is gated on instancez being healthy, so by the time it runs migrations
# have finished and the tables exist. Re-running is safe: the user create
# tolerates a 422, and seed.sql is idempotent. Both psql and wget already ship
# in postgres:17-alpine, so there is nothing to install.
set -eu

API="${INSTANCEZ_URL:-http://instancez:8080}"
SECRET_KEY="${INSTANCEZ_SECRET_KEY:?INSTANCEZ_SECRET_KEY is required}"
DB_URL="${INSTANCEZ_DATABASE_URL:?INSTANCEZ_DATABASE_URL is required}"

echo "seed: waiting for instancez at $API ..."
until wget -q -O /dev/null "$API/ready" 2>/dev/null; do
    sleep 1
done

echo "seed: creating demo user via the admin API ..."
# busybox wget exits non-zero on any 4xx/5xx and writes the status line to
# stderr, so a 422 (user already exists) is told apart from a real failure by
# grepping the log.
if wget -O /tmp/seed-user.json \
    --header="apikey: $SECRET_KEY" \
    --header="Content-Type: application/json" \
    --post-data='{"email":"demo@example.com","password":"demo-password","email_confirm":true,"user_metadata":{"display_name":"Demo User"}}' \
    "$API/auth/v1/admin/users" 2>/tmp/seed-user.err; then
    echo "seed: demo user created (demo@example.com / demo-password)."
elif grep -q ' 422' /tmp/seed-user.err; then
    echo "seed: demo user already exists, continuing."
else
    echo "seed: admin create-user failed:"; cat /tmp/seed-user.err; exit 1
fi

echo "seed: loading the catalog via psql ..."
psql "$DB_URL" -v ON_ERROR_STOP=1 -f /seed.sql

echo "seed: done."
