#!/usr/bin/env python3
"""Reviewed deployment configuration; credentials and backups stay on the host.

Run beside ../docs/operations config files; preview is the default. Targets only
PostgreSQL deployment settings, never changes schemas, keys or user balances.
"""
import argparse
import datetime
import json
import pathlib
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--database', required=True)
parser.add_argument('--apply', action='store_true')
parser.add_argument('--enable-metered', action='store_true', help='Final step only, after all deployment verification and monitoring are ready')
parser.add_argument('--snapshot-dir', default='/opt/ops-backups/team-routing')
args = parser.parse_args()
root = pathlib.Path(__file__).resolve().parents[1]
policy = json.loads((root/'docs/operations/team-routing-policy.json').read_text())
discounts = json.loads((root/'docs/operations/team-cache-discounts.json').read_text())
default_cache = json.loads((root/'docs/operations/default-cache-read.json').read_text())

def quote(value):
    return "'" + str(value).replace("'", "''") + "'"

def sql(statement):
    result = subprocess.run(['docker','exec','-i','new-api-postgres-1','psql','-X','-At','-v','ON_ERROR_STOP=1','-U','newapi','-d',args.database],input=statement,text=True,capture_output=True)
    if result.returncode:
        raise RuntimeError(result.stderr)
    return [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]

channels = sql('BEGIN READ ONLY; SELECT row_to_json(t) FROM (SELECT id,name,status,tag,"group",models,model_mapping,priority,weight FROM channels ORDER BY id) t; COMMIT;')
option_rows = sql("BEGIN READ ONLY; SELECT row_to_json(t) FROM (SELECT key,value FROM options WHERE key IN ('CacheRatio','RetryTimes','AutomaticDisableKeywords','RoutingPolicy','channel_affinity_setting.rules')) t; COMMIT;")
options = {r['key']:r['value'] for r in option_rows}
cache = json.loads(options.get('CacheRatio',json.dumps(default_cache)))
cache_added = {}
models = set(','.join(c['models'] for c in channels if c['status'] != 2 or c['name'].startswith('天翼云-')).split(','))
if any(c['name'].startswith('天翼云-') and 'kimi-k3' in c['models'].split(',') for c in channels):
    models.add('k3')
if sum(c['name'].startswith('天翼云-') for c in channels) != 1:
    raise RuntimeError('Expected exactly one metered channel; review the deployment inventory before continuing')
for name,ratio in discounts.items():
    if name in models and name not in cache:
        cache[name]=ratio
        cache_added[name]=ratio
updates=[]
ctyun_id=None
for row in channels:
    name=row['name']
    tag=None
    if name.startswith('SenseNova-'): tag='sensenova'
    elif name.startswith('火山方舟-'): tag='volcengine'
    elif name.startswith('KimiCode-'): tag='kimi'
    elif name.startswith('天翼云-'): tag='ctyun'
    if not tag:
        if row['status']==1: raise RuntimeError(f"Active channel {row['id']} needs explicit tag mapping")
        continue
    if row['tag'] not in (None,'',tag): raise RuntimeError(f"Channel {row['id']} already has another tag")
    changed={'tag':tag}
    if tag=='ctyun':
        if row['group']!='vip': raise RuntimeError('Metered channel must be VIP-only')
        ctyun_id=row['id']
        names=row['models'].split(',')
        if 'kimi-k3' in names and 'k3' not in names: names.append('k3')
        mapping=json.loads(row['model_mapping'] or '{}')
        if mapping.get('k3') not in (None,'kimi-k3'): raise RuntimeError('Conflicting k3 mapping')
        mapping['k3']='kimi-k3'
        changed.update(models=','.join(names),model_mapping=json.dumps(mapping),status=1 if args.enable_metered else 2)
    updates.append((row,changed))
if ctyun_id is None: raise RuntimeError('Missing configured metered channel')
# Respect all existing whitelist boundaries. No channel is granted a new group.
default_models=set()
for row in channels:
    if row['status']==2: continue
    if 'default' in row['group'].split(','): default_models.update(row['models'].split(','))
if default_models != {'deepseek-v4-flash','sensenova-6.8-flash-lite','codex-auto-review'}: raise RuntimeError('Unexpected default model whitelist')
keywords=options.get('AutomaticDisableKeywords','').splitlines()
quota_terms=[s.lower() for s in policy['quota_error_keywords']]
keywords=[k for k in keywords if not any(term in k.lower() or k.lower() in term for term in quota_terms)]
rules=json.loads(options.get('channel_affinity_setting.rules','[]'))
for rule in rules: rule['skip_retry_on_failure']=False
new_options={
 'RoutingPolicy':json.dumps(policy,separators=(',',':')),
 'RetryTimes':'7',
 'CacheRatio':json.dumps(cache,separators=(',',':')),
 'AutomaticDisableKeywords':'\n'.join(keywords),
 'channel_affinity_setting.rules':json.dumps(rules,separators=(',',':')),
}
print(json.dumps({'database':args.database,'mode':'apply' if args.apply else 'preview','channel_tags':{str(row['id']):changed['tag'] for row,changed in updates},'ctyun_id':ctyun_id,'ctyun_enabled':args.enable_metered,'ctyun_groups':['vip'],'cache_discounts_added':cache_added,'retry_times':7,'policy':policy},ensure_ascii=False,indent=2))
if not args.apply: raise SystemExit(0)
folder=pathlib.Path(args.snapshot_dir)/datetime.datetime.now().strftime('%Y%m%d_%H%M%S')
folder.mkdir(parents=True,mode=0o700)
snapshot=folder/'settings-before.json'
snapshot.write_text(json.dumps({'database':args.database,'channels':channels,'options':option_rows},ensure_ascii=False,indent=2))
snapshot.chmod(0o600)
statements=['BEGIN;','LOCK TABLE channels,abilities,options IN SHARE ROW EXCLUSIVE MODE;']
# Guard concurrent operator changes between preview/read and applying the plan.
for row,changed in updates:
    expected=json.dumps(row,ensure_ascii=False)
    statements.append("DO $$ BEGIN IF (SELECT row_to_json(t)::jsonb FROM (SELECT id,name,status,tag,\"group\",models,model_mapping,priority,weight FROM channels WHERE id="+str(row['id'])+") t) IS DISTINCT FROM "+quote(expected)+"::jsonb THEN RAISE EXCEPTION 'channel changed concurrently'; END IF; END $$;")
    assignments=','.join(k+'='+quote(v) for k,v in changed.items())
    statements.append('UPDATE channels SET '+assignments+' WHERE id='+str(row['id'])+';')
    statements.append('UPDATE abilities SET tag='+quote(changed['tag'])+' WHERE channel_id='+str(row['id'])+';')
for key,value in new_options.items():
    old=options.get(key)
    check='NULL' if old is None else quote(old)
    statements.append('DO $$ BEGIN IF (SELECT value FROM options WHERE key='+quote(key)+') IS DISTINCT FROM '+check+" THEN RAISE EXCEPTION 'option changed concurrently'; END IF; END $$;")
    statements.append('INSERT INTO options(key,value) VALUES('+quote(key)+','+quote(value)+') ON CONFLICT(key) DO UPDATE SET value=excluded.value;')
statements.append('UPDATE abilities SET enabled='+('true' if args.enable_metered else 'false')+' WHERE channel_id='+str(ctyun_id)+';')
statements.append("INSERT INTO abilities(\"group\",model,channel_id,enabled,priority,weight,tag) SELECT 'vip','k3',id,(status=1),priority,weight,tag FROM channels WHERE id="+str(ctyun_id)+" ON CONFLICT(\"group\",model,channel_id) DO UPDATE SET enabled=excluded.enabled,priority=excluded.priority,weight=excluded.weight,tag=excluded.tag;")
statements.append('COMMIT;')
sql('\n'.join(statements))
print('Configuration applied; settings rollback snapshot:',snapshot)
