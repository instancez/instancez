---
title: Ecommerce Store
description: A storefront with a product catalog, per-customer orders, RLS, and Stripe Checkout end to end.
---

A small online store: customers browse a public product catalog, check out through Stripe, and see only their own orders. One code function builds the Stripe Checkout Session and records a pending order; a webhook marks the order paid once Stripe confirms the payment and emails a receipt.

This example brings together most of what instancez does in a single project: auth, tables with RLS, and code functions that call a third-party API.

## instancez.yaml

The whole project lives in one file:

```yaml
# instancez.yaml
version: 1

project:
  name: Storefront
  description: A small online store with Stripe Checkout.

extensions:
  - pgcrypto

auth:
  jwt_expiry: 1h
  refresh_tokens: true
  allow_signup: true

tables:
  products:
    fields:
      - name: id
        type: uuid
        default: uuid_v7()
        primary_key: true
      - name: name
        type: text
        required: true
      - name: description
        type: text
      - name: price_cents
        type: integer
        required: true
        min: 0
      - name: currency
        type: text
        default: "usd"
      - name: active
        type: boolean
        default: true
    rls:
      # Anyone can read products that are for sale.
      - operations: [select]
        check: "active"

  orders:
    fields:
      - name: id
        type: uuid
        default: uuid_v7()
        primary_key: true
      - name: user_id
        type: uuid
        required: true
        foreign_key:
          references: auth.users.id
          on_delete: cascade
      - name: status
        type: text
        enum: [pending, paid, fulfilled, cancelled]
        default: "pending"
      - name: total_cents
        type: integer
        required: true
      - name: currency
        type: text
        default: "usd"
      - name: stripe_session_id
        type: text
      - name: created_at
        type: timestamptz
        default: now()
    rls:
      # A customer reads only their own orders. Writes come from the functions
      # running as service_role (which bypasses RLS), so there's no write policy.
      - operations: [select]
        check: "auth.uid() = user_id"

  order_items:
    fields:
      - name: id
        type: uuid
        default: uuid_v7()
        primary_key: true
      - name: order_id
        type: uuid
        required: true
        foreign_key:
          references: orders.id
          on_delete: cascade
      - name: product_id
        type: uuid
        required: true
        foreign_key:
          references: products.id
      - name: quantity
        type: integer
        required: true
        min: 1
      - name: unit_price_cents
        type: integer
        required: true
    rls:
      # Readable only through an order the caller owns.
      - operations: [select]
        check: "order_id IN (SELECT id FROM orders WHERE user_id = auth.uid())"

functions:
  create-checkout:
    runtime: node
    file: functions/create-checkout.js
    auth_required: true        # you have to be signed in to buy
    timeout: 15s
    env:
      STRIPE_SECRET_KEY: ${INSTANCEZ_ENV_STRIPE_SECRET_KEY}
      CHECKOUT_SUCCESS_URL: ${INSTANCEZ_ENV_CHECKOUT_SUCCESS_URL}
      CHECKOUT_CANCEL_URL: ${INSTANCEZ_ENV_CHECKOUT_CANCEL_URL}

  stripe-webhook:
    runtime: node
    file: functions/stripe-webhook.js
    auth_required: false       # Stripe signs the request, not a logged-in user
    timeout: 10s
    env:
      STRIPE_SECRET_KEY: ${INSTANCEZ_ENV_STRIPE_SECRET_KEY}
      STRIPE_WEBHOOK_SECRET: ${INSTANCEZ_ENV_STRIPE_WEBHOOK_SECRET}
      RESEND_API_KEY: ${INSTANCEZ_ENV_RESEND_API_KEY}
```

A few things worth calling out:

- **Products are public-read**, gated only on `active`. No sign-in needed to browse.
- **Orders are private.** The `select` policy on `orders` and `order_items` means a customer can only ever see their own. Postgres enforces it, not your code.
- **Nobody writes orders directly.** There's no `insert` or `update` policy, so the only path to creating or changing an order is through the functions, which run as `service_role` and bypass RLS. The price a customer pays never comes from the client.

## How a purchase flows

1. The customer signs in and browses `products`.
2. They call `create-checkout` with the items they want.
3. The function prices the cart **server-side**, writes a `pending` order, and returns a Stripe Checkout URL.
4. The customer pays on Stripe's hosted page.
5. Stripe POSTs `checkout.session.completed` to the `stripe-webhook` function, which flips the order to `paid` and sends a receipt.

## Client setup

```js
import { createClient } from '@supabase/supabase-js'

const supabase = createClient('http://localhost:8080', '<your-anon-key>')
```

## Sign up and browse

```js
await supabase.auth.signUp({ email: 'shopper@example.com', password: 'hunter2' })
await supabase.auth.signInWithPassword({ email: 'shopper@example.com', password: 'hunter2' })

// Public catalog: RLS allows reading active products without auth.
const { data: products } = await supabase
  .from('products')
  .select('id, name, description, price_cents, currency')
  .order('name')
```

## Start checkout

```js
const { data, error } = await supabase.functions.invoke('create-checkout', {
  body: {
    items: [
      { product_id: 'PRODUCT_UUID', quantity: 2 },
      { product_id: 'OTHER_UUID', quantity: 1 },
    ],
  },
})

// Send the customer to Stripe's hosted checkout.
window.location.href = data.url
```

## View my orders

```js
// RLS returns only the signed-in customer's orders, with their line items embedded.
const { data: orders } = await supabase
  .from('orders')
  .select('*, order_items(*)')
  .order('created_at', { ascending: false })
```

## The checkout function

```js
// functions/create-checkout.js
import Stripe from 'stripe'

export default async function handler(req, ctx) {
  // Secrets live on ctx.env, never process.env (the worker scrubs the host env).
  const stripe = new Stripe(ctx.env.STRIPE_SECRET_KEY)

  const items = req.body?.items
  if (!Array.isArray(items) || items.length === 0) {
    return { status: 400, body: { error: 'items required' } }
  }

  // Look up real prices. Never trust amounts sent by the client.
  const ids = items.map(i => i.product_id)
  const { data: products, error: lookupErr } = await ctx.serviceClient
    .from('products')
    .select('id, name, price_cents, currency, active')
    .in('id', ids)
  if (lookupErr) {
    ctx.log.error('product lookup failed', { error: lookupErr.message })
    return { status: 500, body: { error: 'could not load products' } }
  }

  const byId = new Map(products.filter(p => p.active).map(p => [p.id, p]))
  const lines = []
  for (const item of items) {
    const product = byId.get(item.product_id)
    if (!product) {
      return { status: 400, body: { error: `unavailable product ${item.product_id}` } }
    }
    lines.push({ product, quantity: Math.max(1, item.quantity | 0) })
  }

  const currency = lines[0].product.currency
  const total = lines.reduce((sum, l) => sum + l.product.price_cents * l.quantity, 0)

  // Record the order as 'pending' first, then attach the Stripe session to it.
  const { data: order, error: orderErr } = await ctx.serviceClient
    .from('orders')
    .insert({ user_id: ctx.claims.sub, status: 'pending', total_cents: total, currency })
    .select('id')
    .single()
  if (orderErr) {
    ctx.log.error('could not create order', { error: orderErr.message })
    return { status: 500, body: { error: 'could not create order' } }
  }

  await ctx.serviceClient.from('order_items').insert(
    lines.map(l => ({
      order_id: order.id,
      product_id: l.product.id,
      quantity: l.quantity,
      unit_price_cents: l.product.price_cents,
    })),
  )

  const session = await stripe.checkout.sessions.create({
    mode: 'payment',
    success_url: ctx.env.CHECKOUT_SUCCESS_URL,
    cancel_url: ctx.env.CHECKOUT_CANCEL_URL,
    metadata: { order_id: order.id },
    line_items: lines.map(l => ({
      quantity: l.quantity,
      price_data: {
        currency,
        unit_amount: l.product.price_cents,
        product_data: { name: l.product.name },
      },
    })),
  })

  await ctx.serviceClient
    .from('orders')
    .update({ stripe_session_id: session.id })
    .eq('id', order.id)

  return { status: 200, body: { url: session.url } }
}
```

`ctx.claims.sub` is the signed-in customer's user ID, taken from the JWT, which is why `auth_required: true` matters. `ctx.serviceClient` is a `service_role` client that bypasses RLS, which is what lets the function write orders that the customer themselves can't write directly.

## The webhook

Stripe signs every webhook payload, and the signature is computed over the exact bytes of the request body. That's why the handler reaches for `req.rawBody` (the unparsed `Buffer`) rather than `req.body`. A re-serialized body won't match the signature, and verification would fail.

```js
// functions/stripe-webhook.js
import Stripe from 'stripe'

export default async function handler(req, ctx) {
  // Secrets live on ctx.env, never process.env (the worker scrubs the host env).
  const stripe = new Stripe(ctx.env.STRIPE_SECRET_KEY)

  const sig = req.headers['stripe-signature']

  let event
  try {
    // req.rawBody is the exact bytes Stripe signed; req.body would not verify.
    event = stripe.webhooks.constructEvent(req.rawBody, sig, ctx.env.STRIPE_WEBHOOK_SECRET)
  } catch (err) {
    ctx.log.warn('Stripe signature verification failed', { error: err.message })
    return { status: 400, body: { error: 'invalid signature' } }
  }

  if (event.type !== 'checkout.session.completed') {
    return { status: 200, body: { received: true } }
  }

  const session = event.data.object
  const orderId = session.metadata.order_id

  // Only flip orders still pending; Stripe may deliver the same event twice.
  const { error } = await ctx.serviceClient
    .from('orders')
    .update({ status: 'paid' })
    .eq('id', orderId)
    .eq('status', 'pending')
  if (error) {
    ctx.log.error('failed to mark order paid', { error: error.message, order: orderId })
    return { status: 500, body: { error: 'database error' } }
  }

  // Send a receipt.
  if (session.customer_details?.email) {
    await fetch('https://api.resend.com/emails', {
      method: 'POST',
      headers: {
        'Authorization': `Bearer ${ctx.env.RESEND_API_KEY}`,
        'Content-Type': 'application/json',
      },
      body: JSON.stringify({
        from: 'orders@yourstore.com',
        to: session.customer_details.email,
        subject: 'Order confirmed',
        html: `<p>Thanks for your order! Total: ${session.amount_total / 100} ${session.currency.toUpperCase()}</p>`,
      }),
    })
  }

  ctx.log.info('order paid', { order: orderId })
  return { status: 200, body: { received: true } }
}
```

## Function dependencies

Both functions import `stripe`, and they use the injected Supabase client, so declare both in `functions/package.json`:

```json
{
  "name": "functions",
  "private": true,
  "type": "module",
  "dependencies": {
    "@supabase/supabase-js": "^2.107.0",
    "stripe": "^17.0.0"
  }
}
```

Run `npm install` in `functions/` once to generate `package-lock.json`, then commit it.

## Secrets

```sh
# .env (gitignored)
INSTANCEZ_ENV_STRIPE_SECRET_KEY=sk_test_...
INSTANCEZ_ENV_STRIPE_WEBHOOK_SECRET=whsec_...
INSTANCEZ_ENV_RESEND_API_KEY=re_...
INSTANCEZ_ENV_CHECKOUT_SUCCESS_URL=https://yourstore.com/thanks
INSTANCEZ_ENV_CHECKOUT_CANCEL_URL=https://yourstore.com/cart
```

## Register the webhook in Stripe

Point a Stripe webhook endpoint at your deployment:

```
https://your-project.instancez.ai/functions/v1/stripe-webhook
```

Select the `checkout.session.completed` event. Locally, use the Stripe CLI to forward events:

```sh
stripe listen --forward-to localhost:8080/functions/v1/stripe-webhook
```

## What to explore next

- Add a `fulfilled` step: a second function (or a dashboard action) that flips a `paid` order to `fulfilled` once it ships.
- Handle `checkout.session.expired` to release abandoned `pending` orders.
- Add an admin-only `insert`/`update` policy on `products` so a store owner can manage the catalog over the API.
- See [Code Functions](/instancez/build/functions/) for the full `req` / `ctx` reference, including `req.rawBody`.
