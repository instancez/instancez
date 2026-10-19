import { createClient } from '@supabase/supabase-js'
import { readFileSync } from 'node:fs'

const cols = JSON.parse(readFileSync(process.env.INZ_CASES, 'utf8'))
const sb = createClient(process.env.INZ_URL, process.env.INZ_KEY, { auth: { persistSession: false } })
const fails = []
const kind = (v) => (v === null ? 'null' : Array.isArray(v) ? 'array' : typeof v)
const must = (cond, msg) => { if (!cond) fails.push(msg) }
const ok = ({ data, error }, what) => { if (error) { fails.push(`${what}: ${JSON.stringify(error)}`); return null } return data }

function checkRow(row, label, nulls) {
  for (const c of cols) {
    const got = nulls && !c.required ? 'null' : kind(row[c.col])
    const want = nulls && !c.required ? 'null' : c.js
    must(got === want, `${label}.${c.col} (${c.sql}): got ${got} (${JSON.stringify(row[c.col])}), want ${want}`)
  }
}

const full = { id: 1, ...Object.fromEntries(cols.map((c) => [c.col, c.sample])) }
const sparse = { id: 2, req_text: 'only' }
ok(await sb.from('rt_all').insert([full, sparse]), 'insert')
const rows = ok(await sb.from('rt_all').select('*').order('id'), 'select') ?? []
must(rows.length === 2, `select rows: ${rows.length}`)
if (rows.length === 2) {
  must(kind(rows[0].id) === 'number', 'id not number')
  checkRow(rows[0], 'rt_all[0]', false)
  checkRow(rows[1], 'rt_all[1]', true)
}

const audit = ok(await sb.schema('audit').from('rt_audit').insert({ id: 1, note: 'n' }).select(), 'audit insert')
must(Array.isArray(audit) && kind(audit[0].id) === 'number' && kind(audit[0].note) === 'string', `audit row: ${JSON.stringify(audit)}`)

const v = await sb.rpc('rt_void')
must(!v.error, `rt_void: ${JSON.stringify(v.error)}`)
must(v.data == null, `rt_void data: ${JSON.stringify(v.data)}`)
const s = ok(await sb.rpc('rt_scalar'), 'rt_scalar')
must(s === 42, `rt_scalar: ${JSON.stringify(s)}`)
const st = ok(await sb.rpc('rt_setof_table'), 'rt_setof_table') ?? []
must(st.length === 2, `rt_setof_table length ${st.length}`)
if (st.length === 2) { checkRow(st[0], 'rt_setof_table[0]', false); checkRow(st[1], 'rt_setof_table[1]', true) }
const si = ok(await sb.rpc('rt_setof_int'), 'rt_setof_int')
must(JSON.stringify(si) === '[{"rt_setof_int":7}]', `rt_setof_int: ${JSON.stringify(si)}`)
const tb = ok(await sb.rpc('rt_table'), 'rt_table')
must(JSON.stringify(tb) === '[{"a":1,"b":"x"}]', `rt_table: ${JSON.stringify(tb)}`)

if (fails.length) { console.error(fails.join('\n')); process.exit(1) }
console.log('runtime types ok')
