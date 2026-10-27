import { createClient } from "@supabase/supabase-js"
import type { Database as Cols } from "./generated/columns"
import type { Database as Full } from "./generated/full"
import { Constants } from "./generated/full"

type Equal<A, B> =
  (<T>() => T extends A ? 1 : 2) extends <T>() => T extends B ? 1 : 2
    ? true
    : false
const assert = <_T extends true>() => {}

const cols = createClient<Cols>("http://localhost", "key")
const full = createClient<Full>("http://localhost", "key")

type Membership = Cols["public"]["Tables"]["membership"]
assert<Equal<Membership["Row"], { org_id: number; role: string; user_id: string }>>()
assert<Equal<Membership["Insert"], { org_id: number; role: string; user_id: string }>>()
assert<Equal<Membership["Update"], { org_id?: number; role?: string; user_id?: string }>>()

type Everything = Cols["public"]["Tables"]["everything"]
assert<Equal<Everything["Row"]["status"], "draft" | "published" | null>>()
assert<Equal<Everything["Insert"]["status"], "draft" | "published" | null | undefined>>()
assert<Equal<Everything["Insert"]["c_integer"], number>>()
assert<Equal<Everything["Row"]["c_json"], import("./generated/columns").Json | null>>()
assert<Equal<Everything["Update"]["created_at"], undefined>>()

type Cats = Full["public"]["Tables"]["cats"]
assert<Equal<Cats["Insert"], { id?: number; name: string }>>()
assert<Equal<Full["audit"]["Tables"]["audit_entries"]["Row"]["id"], number>>()

assert<Equal<typeof Constants.public.Enums, {}>>()
assert<Equal<keyof typeof Constants, "audit" | "public">>()

// selected rows carry the generated shape
async function selected() {
  const { data } = await cols.from("membership").select()
  assert<Equal<typeof data, Membership["Row"][] | null>>()
}
void selected

cols.from("membership").insert({ org_id: 1, user_id: "u", role: "admin" })
cols.from("membership").update({ role: "viewer" })
cols.from("everything").update({ status: "published" })
full.rpc("greet", { who: "a", forced: "b", bare: "c" })
full.rpc("greet", { who: "a", forced: "b", bare: "c", loud: true })

// @ts-expect-error missing required insert key (role)
cols.from("membership").insert({ org_id: 1, user_id: "u" })

// @ts-expect-error immutable column cannot be updated
cols.from("everything").update({ created_at: "2020-01-01" })

// @ts-expect-error wrong enum literal
cols.from("everything").update({ status: "archived" })

// @ts-expect-error unknown table
cols.from("nope")

// @ts-expect-error wrong rpc arg name
full.rpc("greet", { whom: "a", forced: "b", bare: "c" })
