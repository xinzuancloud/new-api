"""Exercise the actual synchronizer shell with isolated, non-network Git/gh fixtures."""
import json
import os
import pathlib
import subprocess
import tempfile
import textwrap
import unittest

ROOT = pathlib.Path(__file__).resolve().parents[2]
STUB = r'''#!/usr/bin/env python3
import json,os,pathlib,sys
name=pathlib.Path(sys.argv[0]).name
args=sys.argv[1:]
with open(os.environ['CALLS'],'a') as f:f.write(json.dumps([name]+args)+'\n')
if name=='gh':
 if args[:2]==['workflow','disable']:
  marker=pathlib.Path('disabled-'+args[2])
  if os.getenv('FAIL_DISABLE')=='1' or os.getenv('ALREADY_DISABLED')=='1' or marker.exists():sys.exit(1)
  marker.touch()
 elif args and args[0]=='api':
  if '/actions/workflows/' in args[1]:
   disabled=os.getenv('ALREADY_DISABLED')=='1' or pathlib.Path('disabled-'+args[1].rsplit('/',1)[1]).exists()
   print('disabled_manually' if disabled and os.getenv('BAD_STATE')!='1' else 'active')
  else:print('2026-09-07T00:00:00Z\tv1.2.3')
 elif args[:2]!=['workflow','run']:raise SystemExit('Unexpected gh call')
elif name=='git':
 if args[0]=='ls-remote':
  ref=args[-1]
  if ref=='refs/heads/main':print('c'*40+'\t'+ref)
  elif os.getenv('EXISTING')=='1':
   if ref.endswith('-fork.*'):print('a'*40+'\trefs/tags/v1.2.3-fork.20260907.t000000.gaaaaaaaaaaaa')
   else:print('b'*40+'\t'+ref)
  elif '--exit-code' in args:sys.exit(2)
 elif args[0]=='rev-parse':
  print('a'*12 if '--short=12' in args else ('b'*40 if 'upstream/' in args[-1] else 'a'*40))
 elif args[0]=='show':print('1788739200')
 elif args[0] not in ('config','remote','fetch','merge','merge-base','push','tag'):raise SystemExit('Unexpected git call')
elif name=='date':print('20260907.t000000')
'''

class ReleaseSyncTest(unittest.TestCase):
    def run_sync(self, **changes):
        source=(ROOT/'.github/workflows/sync-upstream-release.yml').read_text()
        script=textwrap.dedent(source.split('        run: |\n',1)[1])
        with tempfile.TemporaryDirectory() as directory:
            folder=pathlib.Path(directory)
            calls=folder/'calls.jsonl'
            for name in ('git','gh','date'):
                path=folder/name
                path.write_text(STUB)
                path.chmod(0o755)
            env=dict(os.environ,PATH=str(folder)+os.pathsep+os.environ['PATH'],CALLS=str(calls),SYNC_TOKEN='fixture-only',REQUESTED_TAG='v1.2.3',FORK_REPOSITORY='fixture/fork',UPSTREAM_REMOTE='https://example.invalid/upstream',UPSTREAM_REPOSITORY='fixture/upstream')
            env.update(changes)
            result=subprocess.run(['bash','-c',script],cwd=folder,env=env,text=True,capture_output=True)
            commands=[json.loads(line) for line in calls.read_text().splitlines()] if calls.exists() else []
            return result,commands

    def test_publishers_require_explicit_tags_and_have_no_push_trigger(self):
        for name in ('fork-release.yml','fork-docker-build.yml','fork-electron-build.yml'):
            with self.subTest(workflow=name):
                source=(ROOT/'.github/workflows'/name).read_text()
                self.assertIn('  workflow_dispatch:',source)
                self.assertNotIn('\n  push:',source)
                self.assertIn('ref: refs/tags/${{ inputs.tag }}',source)
                self.assertIn('cancel-in-progress: false',source)
        source=(ROOT/'.github/workflows/fork-docker-build.yml').read_text()
        pins=[line.split('@',1)[1].split()[0] for line in source.splitlines() if 'uses: sigstore/cosign-installer@' in line]
        self.assertEqual(len(pins),2)
        self.assertEqual(len(set(pins)),1)
        self.assertEqual(len(pins[0]),40)

    def test_new_tags_publish_once_through_controlled_workflows(self):
        result,calls=self.run_sync()
        self.assertEqual(result.returncode,0,result.stderr)
        disables=[c for c in calls if c[:2]==['gh','workflow'] and c[2]=='disable']
        self.assertEqual({c[3] for c in disables},{'release.yml','docker-build.yml','electron-build.yml'})
        first_push=next(i for i,c in enumerate(calls) if c[:2]==['git','push'])
        self.assertTrue(all(calls.index(c)<first_push for c in disables))
        dispatches=[c for c in calls if c[:3]==['gh','workflow','run']]
        self.assertEqual(len(dispatches),6)
        self.assertEqual({c[3] for c in dispatches},{'fork-release.yml','fork-docker-build.yml','fork-electron-build.yml'})
        self.assertEqual(len({tuple(c) for c in dispatches}),6)
        self.assertTrue(all('--force' not in c for c in calls))

    def test_already_disabled_publishers_allow_recovery_without_disable_request(self):
        result,calls=self.run_sync(EXISTING='1',ALREADY_DISABLED='1')
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertFalse(any(c[:3]==['gh','workflow','disable'] for c in calls))
        self.assertEqual(len([c for c in calls if c[:3]==['gh','workflow','run']]),6)

    def test_manual_recovery_uses_existing_tag_names_without_rewriting_tags(self):
        result,calls=self.run_sync(EXISTING='1')
        self.assertEqual(result.returncode,0,result.stderr)
        dispatches=[c for c in calls if c[:3]==['gh','workflow','run']]
        self.assertEqual(len(dispatches),6)
        self.assertTrue(any('tag=v1.2.3-fork.20260907.t000000.gaaaaaaaaaaaa' in c for c in dispatches))
        self.assertFalse(any(c[:2]==['git','tag'] or (c[:2]==['git','push'] and any('refs/tags/' in a for a in c)) for c in calls))

    def test_scheduled_already_synchronized_release_does_not_republish(self):
        result,calls=self.run_sync(EXISTING='1',REQUESTED_TAG='')
        self.assertEqual(result.returncode,0,result.stderr)
        self.assertFalse(any(c[:3]==['gh','workflow','run'] for c in calls))
        self.assertFalse(any(c[:2]==['git','push'] for c in calls))

    def test_cannot_push_when_legacy_workflows_cannot_be_disabled(self):
        for change in ({'FAIL_DISABLE':'1'},{'BAD_STATE':'1'},{'SYNC_TOKEN':''}):
            with self.subTest(change=change):
                result,calls=self.run_sync(**change)
                self.assertNotEqual(result.returncode,0)
                self.assertFalse(any(c[:2]==['git','push'] for c in calls))
                self.assertFalse(any(c[:3]==['gh','workflow','run'] for c in calls))

if __name__=='__main__':unittest.main()
