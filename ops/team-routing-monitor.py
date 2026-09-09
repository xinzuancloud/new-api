#!/usr/bin/env python3
"""Passive local audit. Emits only aggregates, never credentials or log bodies.

Run on the deployment host. Does not send model requests or mutate application
settings. HTTP status is supplemented by stream outcome metadata (200 != complete).
"""
import argparse
import collections
import datetime
import json
import pathlib
import subprocess
import time

parser=argparse.ArgumentParser()
parser.add_argument('--minutes',type=int,default=60)
parser.add_argument('--output')
args=parser.parse_args()
if not 1<=args.minutes<=1440: raise SystemExit('minutes must be 1..1440')
end=int(time.time())-60  # Allow trailing requests/settlement a short grace period.
start=end-args.minutes*60
q=f"""
BEGIN READ ONLY;
SET LOCAL statement_timeout='15s';
SELECT json_build_object('kind','routes','group',l."group",'tag',coalesce(c.tag,'untagged'),'model',model_name,'consume_logs',count(*),'quota',sum(quota),'input_tokens',sum(prompt_tokens),'cache_read_tokens',sum(coalesce((nullif(l.other,'')::jsonb->>'cache_tokens')::numeric,0)),'p95_seconds',percentile_cont(.95) WITHIN GROUP(ORDER BY use_time),'incomplete_streams',count(*) FILTER(WHERE nullif(l.other,'')::jsonb#>>'{{stream_status,status}}'='error'),'stream_metadata_missing',count(*) FILTER(WHERE is_stream AND nullif(l.other,'')::jsonb->'stream_status' IS NULL)) FROM logs l LEFT JOIN channels c ON c.id=l.channel_id WHERE l.type=2 AND created_at BETWEEN {start} AND {end} GROUP BY l."group",c.tag,model_name;
SELECT json_build_object('kind','attempts','channel',channel_id,'type',type,'count',count(*)) FROM logs l WHERE type IN(2,5) AND created_at BETWEEN {start} AND {end} GROUP BY channel_id,type;
WITH requests AS(SELECT request_id,bool_or(type=2) AS consumed,count(*) FILTER(WHERE type=5) AS errors FROM logs l WHERE type IN(2,5) AND request_id<>'' AND created_at BETWEEN {start} AND {end} GROUP BY request_id) SELECT json_build_object('kind','correlation','requests',count(*),'recovered_after_errors',count(*) FILTER(WHERE consumed AND errors>0),'error_only_requests',count(*) FILTER(WHERE NOT consumed),'error_attempts',sum(errors)) FROM requests;
SELECT json_build_object('kind','stream_end','reason',nullif(l.other,'')::jsonb#>>'{{stream_status,end_reason}}','count',count(*)) FROM logs l WHERE l.type=2 AND is_stream AND created_at BETWEEN {start} AND {end} GROUP BY 2;
SELECT json_build_object('kind','protocol_routes','source',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,source}}','target',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,target}}','requests',count(*),'incomplete_streams',count(*) FILTER(WHERE nullif(l.other,'')::jsonb#>>'{{stream_status,status}}'='error'),'skipped_candidates',sum(CASE WHEN jsonb_typeof(nullif(l.other,'')::jsonb#>'{{admin_info,protocol_skips}}')='array' THEN jsonb_array_length(nullif(l.other,'')::jsonb#>'{{admin_info,protocol_skips}}') ELSE 0 END)) FROM logs l WHERE l.type=2 AND created_at BETWEEN {start} AND {end} AND nullif(l.other,'')::jsonb#>'{{admin_info,protocol_route}}' IS NOT NULL GROUP BY nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,source}}',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,target}}';
WITH native_empty_stream_requests AS (
 SELECT request_id,
  max((nullif(other,'')::jsonb#>>'{{admin_info,protocol_native_empty_stream_retries}}')::integer) AS retry_attempts,
  bool_or(type=2 AND nullif(other,'')::jsonb#>>'{{stream_status,status}}'='ok') AS completed,
  bool_or(type=2 AND nullif(other,'')::jsonb#>>'{{stream_status,status}}'='error') AS incomplete
 FROM logs
 WHERE type IN(2,5) AND request_id<>'' AND created_at BETWEEN {start} AND {end}
  AND nullif(other,'')::jsonb#>>'{{admin_info,protocol_native_empty_stream_retries}}' IS NOT NULL
 GROUP BY request_id
)
SELECT json_build_object('kind','native_empty_stream_retries','requests',count(*),
 'retry_attempts',coalesce(sum(retry_attempts),0),
 'completed_requests',count(*) FILTER(WHERE completed),
 'error_only_requests',count(*) FILTER(WHERE NOT completed),
 'incomplete_streams',count(*) FILTER(WHERE incomplete))
FROM native_empty_stream_requests;
WITH auto_review_requests AS (
 SELECT request_id,
  bool_or(type=2) AS completed,
  count(*) FILTER(WHERE type=5) AS error_attempts,
  count(*) FILTER(WHERE type=2 AND nullif(other,'')::jsonb#>>'{{stream_status,status}}'='error') AS incomplete_streams
 FROM logs
 WHERE model_name='codex-auto-review' AND type IN(2,5) AND request_id<>''
  AND created_at BETWEEN {start} AND {end}
 GROUP BY request_id
)
SELECT json_build_object('kind','auto_review_correlation','requests',count(*),
 'completed_requests',count(*) FILTER(WHERE completed),
 'recovered_after_errors',count(*) FILTER(WHERE completed AND error_attempts>0),
 'error_only_requests',count(*) FILTER(WHERE NOT completed),
 'error_attempts',sum(error_attempts),'incomplete_streams',sum(incomplete_streams))
FROM auto_review_requests;
SELECT json_build_object('kind','auto_review_routes','group',l."group",'tag',coalesce(c.tag,'untagged'),
 'source',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,source}}',
 'target',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,target}}',
 'requests',count(*),'incomplete_streams',count(*) FILTER(WHERE nullif(l.other,'')::jsonb#>>'{{stream_status,status}}'='error'))
FROM logs l LEFT JOIN channels c ON c.id=l.channel_id
WHERE l.type=2 AND l.model_name='codex-auto-review' AND l.created_at BETWEEN {start} AND {end}
GROUP BY l."group",c.tag,nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,source}}',
 nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,target}}';
SELECT json_build_object('kind','policy','key',key,'value',value) FROM options WHERE key='RoutingPolicy';
SELECT json_build_object('kind','health','tag',tag,'status',status,'channels',count(*)) FROM channels GROUP BY tag,status;
SELECT json_build_object(
 'kind','search_tool_charges','group',l."group",'tag',c.tag,'model',l.model_name,
 'source',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,source}}',
 'target',nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,target}}',
 'tool',t.item->>'name',
 'configured_price_per_1k',CASE WHEN jsonb_typeof(t.item->'price')='number' THEN t.item->'price' ELSE NULL END,
 'priced_calls',sum(CASE WHEN jsonb_typeof(t.item->'count')='number' THEN (t.item->>'count')::numeric ELSE 0 END),
 'consume_logs',count(*),
 'incomplete_streams',count(*) FILTER(WHERE nullif(l.other,'')::jsonb#>>'{{stream_status,status}}'='error'))
FROM logs l LEFT JOIN channels c ON c.id=l.channel_id
CROSS JOIN LATERAL jsonb_array_elements(CASE
 WHEN jsonb_typeof(nullif(l.other,'')::jsonb->'tool_surcharges')='array'
 THEN nullif(l.other,'')::jsonb->'tool_surcharges' ELSE '[]'::jsonb END) t(item)
WHERE l.type=2 AND created_at BETWEEN {start} AND {end}
 AND t.item->>'name' IN ('web_search','web_search_preview')
GROUP BY l."group",c.tag,l.model_name,
 nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,source}}',
 nullif(l.other,'')::jsonb#>>'{{admin_info,protocol_route,target}}',t.item->>'name',
 CASE WHEN jsonb_typeof(t.item->'price')='number' THEN t.item->'price' ELSE NULL END;
COMMIT;
"""
# Group by the end-reason expression, not the json aggregate result.
q=q.replace('GROUP BY 2;','GROUP BY nullif(l.other,\'\')::jsonb#>>\'{stream_status,end_reason}\';')
p=subprocess.run(['docker','exec','-i','new-api-postgres-1','psql','-X','-At','-v','ON_ERROR_STOP=1','-U','newapi','-d','new-api'],input=q,text=True,capture_output=True)
if p.returncode: raise SystemExit(p.stderr)
rows=[json.loads(line) for line in p.stdout.splitlines() if line.startswith('{')]
http=collections.Counter()
durations=[]
access=pathlib.Path('/opt/new-api/caddy-logs/access.log')
if access.exists():
    with access.open() as f:
        for line in f:
            try: entry=json.loads(line)
            except ValueError: continue
            if not start<=entry.get('ts',0)<=end: continue
            path=entry.get('request',{}).get('uri','').split('?',1)[0]
            if not path.startswith(('/v1/','/v1beta/')): continue
            if entry.get('request',{}).get('method')!='POST':continue
            http[str(entry.get('status',0))]+=1
            durations.append(entry.get('duration',0))
alerts=[]
policy=next((json.loads(r['value']) for r in rows if r['kind']=='policy'),{})
for row in rows:
    if row['kind']=='routes' and row['group'] in policy.get('group_tag_order',{}) and row['tag'] not in policy['group_tag_order'][row['group']]:
        alerts.append({'severity':'critical','reason':'route outside configured group tags','group':row['group'],'tag':row['tag'],'count':row['consume_logs']})
    if row['kind']=='routes' and row['incomplete_streams']:
        alerts.append({'severity':'review','reason':'incomplete streams','group':row['group'],'tag':row['tag'],'count':row['incomplete_streams']})
    if row['kind']=='auto_review_correlation' and row['error_only_requests']:
        alerts.append({'severity':'review','reason':'auto-review requests exhausted all candidates','count':row['error_only_requests']})
    if row['kind']=='auto_review_correlation' and row['incomplete_streams']:
        alerts.append({'severity':'review','reason':'auto-review incomplete streams','count':row['incomplete_streams']})
    if row['kind']=='native_empty_stream_retries' and row['error_only_requests']:
        alerts.append({'severity':'review','reason':'native empty-stream retries did not recover','count':row['error_only_requests']})
result={'observed_at':datetime.datetime.now(datetime.timezone.utc).isoformat(),'window_start':start,'window_end':end,'window_minutes':args.minutes,'http_status_counts':dict(http),'http_sample_count':len(durations),'http_p95_seconds':sorted(durations)[min(len(durations)-1,int(len(durations)*.95))] if durations else None,'aggregates':rows,'alerts':alerts,'interpretation':'Quota is internal ledger units, not supplier invoice. Error-only correlated requests are not a definitive final failure rate. Native empty-stream retries are safe retries before any client bytes. Access log sample covers current log file; stream metadata supplements HTTP status.'}
encoded=json.dumps(result,ensure_ascii=False,indent=2)
if args.output:
    output=pathlib.Path(args.output)
    output.parent.mkdir(parents=True,exist_ok=True,mode=0o700)
    output.write_text(encoded+'\n')
    output.chmod(0o600)
print(encoded)
