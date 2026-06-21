-- Catalog seed data for the gearstore demo.
--
-- This runs over a direct superuser connection (see seed.sh), so it bypasses
-- RLS the same way a privileged migration or admin job would. instancez no
-- longer seeds from YAML; the two supported paths are the admin API (used in
-- seed.sh for the demo user) and plain SQL like this. Foreign keys resolve
-- with subqueries, so there are no ids to thread through a script.
--
-- Every block is safe to re-run: categories upsert on their unique slug, and
-- the products/reviews inserts no-op once their tables hold any rows.

INSERT INTO categories (slug, name) VALUES
    ('keyboards', 'Keyboards'),
    ('mice',      'Mice & Trackballs'),
    ('monitors',  'Monitors'),
    ('audio',     'Audio'),
    ('desks',     'Desks & Mounts')
ON CONFLICT (slug) DO NOTHING;

INSERT INTO products (name, description, price_cents, stock, status, featured, on_sale, tags, metadata, category_id)
SELECT v.name, v.description, v.price_cents, v.stock, v.status, v.featured, v.on_sale, v.tags, v.metadata,
       (SELECT id FROM categories WHERE slug = v.slug)
FROM (VALUES
    ('Aurora 75 Mechanical Keyboard', 'Hot-swappable 75% board with a gasket mount and PBT caps.',
        12900, 40, 'active', true,  false, ARRAY['mechanical','wireless'], '{"brand":"Aurora","layout":"75%"}'::jsonb, 'keyboards'),
    ('Vela Low-Profile Keyboard', 'Slim scissor-switch board built for long typing days.',
        8900, 60, 'active', false, true,  ARRAY['low-profile','quiet'],     '{"brand":"Vela","layout":"TKL"}'::jsonb,   'keyboards'),
    ('Drift Wireless Trackball', 'Thumb-operated trackball that frees up desk space.',
        6500, 35, 'active', false, false, ARRAY['ergonomic','wireless'],    '{"brand":"Drift","dpi":2400}'::jsonb,      'mice'),
    ('Pulse Ergo Mouse', 'Vertical grip mouse that keeps the wrist neutral.',
        5400, 50, 'active', false, true,  ARRAY['ergonomic'],               '{"brand":"Pulse","dpi":3200}'::jsonb,      'mice'),
    ('Nimbus 27 4K Monitor', 'A 27-inch 4K IPS panel with a factory color profile.',
        38900, 18, 'active', true,  false, ARRAY['4k','ips'],               '{"brand":"Nimbus","size_in":27}'::jsonb,   'monitors'),
    ('Halo Ultrawide 34', '34-inch curved ultrawide for side-by-side windows.',
        49900, 12, 'active', false, false, ARRAY['ultrawide','curved'],     '{"brand":"Halo","size_in":34}'::jsonb,     'monitors'),
    ('Sonata Desk Speakers', 'Compact stereo pair tuned for a near-field desk setup.',
        15900, 25, 'active', false, true,  ARRAY['stereo','bluetooth'],     '{"brand":"Sonata","watts":40}'::jsonb,     'audio'),
    ('Vertex Standing Desk', 'Dual-motor sit-stand frame with four height presets.',
        59900, 8,  'active', true,  false, ARRAY['standing','motorized'],   '{"brand":"Vertex","width_cm":140}'::jsonb, 'desks')
) AS v(name, description, price_cents, stock, status, featured, on_sale, tags, metadata, slug)
WHERE NOT EXISTS (SELECT 1 FROM products);

-- A few reviews. The ones flagged `mine` are attributed to the demo user that
-- seed.sh creates, so the FK to auth.users.id is exercised; the rest are
-- left anonymous (user_id stays null) with just an author name.
INSERT INTO reviews (product_id, user_id, author, rating, body)
SELECT (SELECT id FROM products WHERE name = v.pname),
       CASE WHEN v.mine THEN (SELECT id FROM auth.users WHERE email = 'demo@example.com') END,
       v.author, v.rating, v.body
FROM (VALUES
    ('Aurora 75 Mechanical Keyboard', 'Demo User', 5, 'Crisp and quiet, exactly what my desk needed.',            true),
    ('Nimbus 27 4K Monitor',          'Demo User', 4, 'Gorgeous panel; the stand wobbles a little.',             true),
    ('Pulse Ergo Mouse',              'Priya R.',  5, 'My wrist stopped aching after a week.',                   false),
    ('Vertex Standing Desk',          'Marco T.',  4, 'Solid frame, though assembly took the better part of an hour.', false)
) AS v(pname, author, rating, body, mine)
WHERE NOT EXISTS (SELECT 1 FROM reviews);
