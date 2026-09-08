#!/usr/bin/env python3
"""Apply reviewed protocol policies to the deployed PostgreSQL settings JSON.

Input: {"channels": {"45": <protocol_routing object>, ...}}. Dry-run by
 default. Snapshots contain only old protocol policies, never channel keys,
 proxies or unrelated settings. Other setting keys and all prices are preserved.
"""
import argparse
import datetime
import json
import os
import pathlib
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('policy_file')
parser.add_argument('--apply', action='store_true')
parser.add_argument('--container', default='new-api-postgres-1')
parser.add_argument('--database', default='new-api')
parser.add_argument('--db-user', default='newapi')
parser.add_argument('--snapshot-dir', default='/opt/ops-backups/protocol-routing')
args = parser.parse_args()
policies = json.loads(pathlib.Path(args.policy_file).read_text())['channels']
ids = [int(value) for value in policies]
if not ids or len(ids) > 256 or any(value <= 0 for value in ids):
    raise SystemExit('Expected 1 to 256 positive channel IDs')
command = ['docker', 'exec', '-i', args.container, 'psql', '-X', '-At', '-v', 'ON_ERROR_STOP=1', '-U', args.db_user, '-d', args.database]

def query(sql):
    result = subprocess.run(command, input=sql, text=True, capture_output=True)
    if result.returncode:
        raise SystemExit('Database operation failed; no sensitive SQL or settings printed')
    return [json.loads(line) for line in result.stdout.splitlines() if line.startswith('{')]

def literal(value):
    if value is None:
        return 'NULL'
    return "'" + value.replace("'", "''") + "'"

rows = query('BEGIN READ ONLY; SELECT json_build_object(\'id\',id,\'setting\',setting) FROM channels WHERE id IN (' + ','.join(map(str, ids)) + '); COMMIT;')
if {row['id'] for row in rows} != set(ids):
    raise SystemExit('A configured channel does not exist')
snapshot = {'observed_at': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'channels': {}}
changes = []
for row in rows:
    current = json.loads(row['setting'] or '{}')
    policy = policies[str(row['id'])]
    if not isinstance(policy, dict) or not isinstance(policy.get('enabled'), bool):
        raise SystemExit('Each policy must be a validated object with enabled')
    snapshot['channels'][str(row['id'])] = {'present': 'protocol_routing' in current, 'protocol_routing': current.get('protocol_routing')}
    updated = dict(current, protocol_routing=policy)
    if updated != current:
        changes.append((row, json.dumps(updated, ensure_ascii=False, separators=(',', ':'))))
print(json.dumps({'mode': 'apply' if args.apply else 'preview', 'channel_ids': [row['id'] for row, _ in changes], 'changed_channels': len(changes)}, ensure_ascii=False))
if not args.apply or not changes:
    raise SystemExit(0)
folder = pathlib.Path(args.snapshot_dir)
folder.mkdir(parents=True, exist_ok=True, mode=0o700)
path = folder / (datetime.datetime.now(datetime.timezone.utc).strftime('%Y%m%dT%H%M%S%fZ') + '.json')
fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
with os.fdopen(fd, 'w') as output:
    json.dump(snapshot, output, ensure_ascii=False, indent=2)
statements = ["BEGIN; SET LOCAL statement_timeout='15s'; SET LOCAL standard_conforming_strings=on;"]
for row, encoded in changes:
    statements.append('UPDATE channels SET setting=' + literal(encoded) + ' WHERE id=' + str(row['id']) + ' AND setting IS NOT DISTINCT FROM ' + literal(row['setting']) + ' RETURNING id AS protocol_updated\n\\gset')

statements.append('COMMIT;')
query('\n'.join(statements))
print(json.dumps({'applied': len(changes), 'snapshot': str(path)}))
