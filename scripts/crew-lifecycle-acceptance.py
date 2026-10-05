"""Local Crew worker/backend restart and budget failure acceptance; no browser access."""
import argparse
import json
import subprocess
import time
import uuid
import urllib.request
from pathlib import Path

parser = argparse.ArgumentParser()
parser.add_argument('--auth-file', required=True)
parser.add_argument('--output', required=True)
parser.add_argument('--compose-env', required=True)
parser.add_argument('--compose-override', required=True)
parser.add_argument('--budget-only', action='store_true', help='Skip container restart checks')
parser.add_argument('--existing-budget-run', help='Validate a retained budget failure fixture instead of creating another model run')
args = parser.parse_args()
auth = json.loads(Path(args.auth_file).read_text())
opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))
evidence = {'checks': []}
def call(method, path, body=None):
    request = urllib.request.Request('http://127.0.0.1:3000/api' + path,
        data=None if body is None else json.dumps(body).encode(), method=method,
        headers={'Cookie': auth['admin'], 'Content-Type': 'application/json'})
    with opener.open(request, timeout=30) as response:
        wire = json.load(response)
    assert wire['code'] == 0, wire.get('reason')
    return wire['data']
def check(name, ok):
    evidence['checks'].append({'name': name, 'pass': bool(ok)})
    print(('PASS ' if ok else 'FAIL ') + name, flush=True)
    assert ok, name
def wait(run_id, predicate, timeout=300):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        run = call('GET', '/agent/crew-runs/' + run_id)
        state = (run['status'], tuple(m['status'] for m in run['members']))
        if last != state:
            print('STATE', state, flush=True)
            last = state
        if predicate(run): return run
        time.sleep(1)
    raise AssertionError('Lifecycle deadline exceeded')
def member(name, role, position, steps=30):
    return {'name': name, 'role': role, 'permissionMode': 'propose', 'enabled': True,
        'position': position, 'focusNodeIds': [], 'modelConfig': {'model': 'deepseek-flash', 'channelId': 'CHANNEL_000001', 'channelModelKey': 'deepseek-flash'},
        'budget': {'maxCredits': 20, 'maxSteps': steps, 'maxGenerationTasks': 0, 'maxVideoSeconds': 0}}
def create(steps=30):
    crew = call('POST', '/agent/workspaces/' + auth['canvasId'] + '/crews', {'name': 'Lifecycle fixture', 'description': '', 'status': 'enabled', 'members': [member('Coordinator', 'coordinator', 0), member('A', 'member', 1, steps), member('B', 'member', 2, steps)]})
    request = {'prompt': '功能验收：必须同时delegate_task给两位成员。成员必须先canvas_get_state再task_result，返回summary和artifactIds=[]，不需要画布proposal。Coordinator用crew_wait收齐结果，再crew_propose审批。禁止直接finish_run。', 'skillIds': [], 'idempotencyKey': 'lifecycle-' + uuid.uuid4().hex, 'maxConcurrentMembers': 2}
    return call('POST', '/agent/crews/' + crew['id'] + '/runs', request)
def restart(service):
    # Only the isolated local test project; volumes and other projects are untouched.
    subprocess.run(['docker', 'compose', '--env-file', args.compose_env, '-p', 'canvas-crew-3000', '-f', 'docker-compose.yml', '-f', args.compose_override, '--profile', 'pi', 'restart', service], check=True)

before = call('GET', '/admin/settings/features')['features']['agentCrewEnabled']
try:
    call('PATCH', '/admin/settings/features', {'agentCrewEnabled': True})
    nodes = call('GET', '/canvas-projects/' + auth['canvasId'])['project']['nodes']
    if not args.budget_only:
        run = create()
        evidence['recoveryRunId'] = run['id']
        wait(run['id'], lambda r: sum(m['role'] == 'member' and m['status'] == 'running' for m in r['members']) == 2, 120)
        check('restart exercised two active members', True)
        restart('agent')
        recovered = wait(run['id'], lambda r: r['status'] in ('waiting_approval', 'failed', 'cancelled', 'completed'))
        check('worker restart recovers real Crew approval', recovered['status'] == 'waiting_approval')
        restart('backend')
        deadline = time.monotonic() + 60
        ready = False
        while time.monotonic() < deadline:
            try:
                ready = bool(call('GET', '/health')['ready'])
                if ready: break
            except Exception: pass
            time.sleep(1)
        check('backend restart becomes ready', ready)
        persisted = call('GET', '/agent/crew-runs/' + run['id'])
        check('approval identity survives backend restart', persisted['status'] == 'waiting_approval' and persisted['approval']['approvalId'] == recovered['approval']['approvalId'])
        call('POST', '/agent/crew-runs/' + run['id'] + '/approvals/' + persisted['approval']['approvalId'], {'decision': 'reject', 'reason': 'Lifecycle fixture cleanup'})
    failed = call('GET', '/agent/crew-runs/' + args.existing_budget_run) if args.existing_budget_run else create(1)
    evidence['budgetFailureRunId'] = failed['id']
    failed = wait(failed['id'], lambda r: r['status'] in ('waiting_approval', 'failed', 'cancelled', 'completed'))
    evidence['failureMembers'] = [{'id': m['id'], 'status': m['status'], 'errorCode': m.get('errorCode')} for m in failed['members']]
    check('member budget failure remains visible to Coordinator', any(m['role'] == 'member' and m['status'] == 'failed' and m.get('errorCode') for m in failed['members']))
    # A failed member may yield a degraded proposal; this still needs user approval.
    if failed['status'] == 'waiting_approval':
        check('degraded proposal remains uncommitted', not failed['approval'].get('operationId'))
        call('POST', '/agent/crew-runs/' + failed['id'] + '/approvals/' + failed['approval']['approvalId'], {'decision': 'reject', 'reason': 'Budget fixture cleanup'})
    check('recovery and failure cannot change canvas', call('GET', '/canvas-projects/' + auth['canvasId'])['project']['nodes'] == nodes)
finally:
    call('PATCH', '/admin/settings/features', {'agentCrewEnabled': before})
    Path(args.output).write_text(json.dumps(evidence, indent=2), encoding='utf-8')
