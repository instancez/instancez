---
title: Webhook Handler
description: A code function that receives a Stripe webhook, records the event, and sends a notification email.
---

A real-world code function pattern: receive an inbound webhook from a third-party service, verify the signature, write a record to the database with elevated privileges, then call an external API using a secret. All without exposing any of this logic to the client.

## What it does

1. Stripe POSTs a `checkout.session.completed` event to `/functions/v1/stripe-webhook`
2. The function verifies the Stripe signature using a secret from `ctx.env`
3. It inserts an `orders` row via `ctx.serviceClient` (bypasses RLS — the function acts as the system, not as a user)
4. It calls Resend to send a confirmation email

## Schema

```yaml
# instancez.yaml
tables:
  orders:
    columns:
      id:           { type: uuid, default: gen_random_uuid(), primary_key: true }
      user_id:      { type: uuid, nullable: false }
      stripe_session: { type: text, nullable: false }
      amount:       { type: integer, nullable: false }
      currency:     { type: text, nullable: false }
      email:        { type: text, nullable: false }
      created_at:   { type: timestamptz, default: now() }
    rls:
      - operations: [select]
        check: "auth.uid() = user_id"
```

The RLS policy lets users read their own orders. Inserts come from the function via `service_role`, so no insert policy is needed.

## Function declaration

```yaml
# instancez.yaml
functions:
  stripe-webhook:
    runtime: node
    file: functions/stripe-webhook.js
    auth_required: false   # Stripe signs the request, not the user
    timeout: 10s
    env:
      STRIPE_WEBHOOK_SECRET: ${INSTANCEZ_ENV_STRIPE_WEBHOOK_SECRET}
      RESEND_API_KEY: ${INSTANCEZ_ENV_RESEND_API_KEY}
```

## Handler

```js
// functions/stripe-webhook.js
import Stripe from 'stripe'

const stripe = new Stripe(process.env.STRIPE_SECRET_KEY)

export default async function handler(req, ctx) {
  const sig = req.headers['stripe-signature']

  let event
  try {
    event = stripe.webhooks.constructEvent(req.body, sig, ctx.env.STRIPE_WEBHOOK_SECRET)
  } catch (err) {
    ctx.log.warn('Stripe signature verification failed', { error: err.message })
    return { status: 400, body: { error: 'Invalid signature' } }
  }

  if (event.type !== 'checkout.session.completed') {
    return { status: 200, body: { received: true } }
  }

  const session = event.data.object

  // Insert as service_role — bypasses RLS
  const { error } = await ctx.serviceClient
    .from('orders')
    .insert({
      user_id:        session.metadata.user_id,
      stripe_session: session.id,
      amount:         session.amount_total,
      currency:       session.currency,
      email:          session.customer_details.email,
    })

  if (error) {
    ctx.log.error('Failed to insert order', { error: error.message, session: session.id })
    return { status: 500, body: { error: 'Database error' } }
  }

  // Send confirmation email
  await fetch('https://api.resend.com/emails', {
    method: 'POST',
    headers: {
      'Authorization': `Bearer ${ctx.env.RESEND_API_KEY}`,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({
      from: 'orders@yourapp.com',
      to: session.customer_details.email,
      subject: 'Order confirmed',
      html: `<p>Thanks for your order! Total: ${session.amount_total / 100} ${session.currency.toUpperCase()}</p>`,
    }),
  })

  ctx.log.info('Order recorded and email sent', { session: session.id })
  return { status: 200, body: { received: true } }
}
```

## Environment variables

```sh
# .env (gitignored)
INSTANCEZ_ENV_STRIPE_WEBHOOK_SECRET=whsec_...
INSTANCEZ_ENV_RESEND_API_KEY=re_...
```

## Register the webhook in Stripe

Point the webhook to your instancez deployment:

```
https://your-project.instancez.io/functions/v1/stripe-webhook
```

Select the `checkout.session.completed` event. Stripe will POST signed payloads there on every completed checkout.

## What to explore next

- Add `ctx.supabase` (caller's client) if you want to do things on behalf of a specific user instead of the system
- Handle additional event types with a `switch` on `event.type`
- See [Code Functions](/instancez/build/functions/) for the full `req` / `ctx` reference
