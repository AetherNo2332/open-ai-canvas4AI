"""Generate a source inventory, not a claim of migration or test coverage."""
import json
import re
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
BASELINE = json.loads((ROOT / 'docs/upstream-baseline.json').read_text(encoding='utf-8'))


def collect():
    entries = {}

    def add(feature_id, source, kind, details=None, agent_only=False):
        entries[feature_id] = {
            'featureId': feature_id, 'kind': kind, 'originalEntry': source,
            'migrationEntry': None, 'mcpTool': None,
            'mcpNotApplicableReason': '内置 Agent 入口由外部 MCP 客户端替代' if agent_only else None,
            'hostStatus': 'pending', 'guestStatus': 'pending', 'acceptanceEvidence': [],
            'agentOnly': agent_only, 'details': details or {},
        }

    source = 'web/src/types/canvas.ts'
    enum = re.search(r'export enum CanvasNodeType\s*\{(.*?)\}', (ROOT / source).read_text(encoding='utf-8'), re.S)[1]
    for name, value in re.findall(r'(\w+)\s*=\s*"([^"]+)"', enum):
        add('node.' + value, source, 'node', {'enum': name})
    for path in sorted((ROOT / 'web/src/lib/canvas/tool-registry/definitions').glob('*.tsx')):
        text = path.read_text(encoding='utf-8')
        for tool in re.findall(r'\bid:\s*"([^"]+)"', text):
            add('tool.' + tool, path.relative_to(ROOT).as_posix(), 'toolbar-tool', agent_only='agent' in tool)
    path = ROOT / 'web/src/lib/canvas/tool-registry/tool-definition.ts'
    for callback in sorted(set(re.findall(r'^\s*(on[A-Z]\w+)\??:', path.read_text(encoding='utf-8'), re.M))):
        add('callback.' + callback, path.relative_to(ROOT).as_posix(), 'toolbar-callback', agent_only=callback == 'onSendSelectionToAgent')
    for directory in ['web/src/pages/canvas', 'web/src/components/canvas', 'web/src/lib/canvas', 'web/src/lib/timeline']:
        for path in sorted((ROOT / directory).glob('*.ts*')):
            if any(key in path.stem for key in ('use-canvas-', 'drawing', 'timeline', 'export', 'import', 'panorama', 'color-grade', 'batch-table', 'previs')):
                add('module.' + path.relative_to(ROOT).as_posix(), path.relative_to(ROOT).as_posix(), 'domain-module', agent_only=path.stem == 'use-canvas-assistant-visibility')
    for path in sorted((ROOT / 'backend/internal/canvas/capability').glob('*.go')):
        if path.name.endswith('_test.go'):
            continue
        text = path.read_text(encoding='utf-8')
        for value in sorted(set(re.findall(r'\bType:\s*"([^"]+)"', text))):
            add('descriptor.' + value, path.relative_to(ROOT).as_posix(), 'backend-descriptor')
    for path in sorted((ROOT / 'plugin-packages').glob('*/manifest.json')):
        manifest = json.loads(path.read_text(encoding='utf-8'))
        for category, contributions in manifest.get('contributes', {}).items():
            items = contributions if isinstance(contributions, list) else [contributions]
            for index, item in enumerate(items):
                item_id = item.get('id', str(index)) if isinstance(item, dict) else str(index)
                scopes = item.get('scopes', []) if isinstance(item, dict) else []
                agent_only = bool(scopes) and all(scope == 'agent' for scope in scopes)
                add(f'plugin.{manifest["id"]}.{category}.{item_id}', path.relative_to(ROOT).as_posix(), 'plugin-contribution', {'pluginId': manifest['id'], 'category': category, 'scopes': scopes}, agent_only)
    # Editors embed additional tools not represented by toolbar registries.
    for directory in ['web/src/components/canvas/previs', 'web/src/components/timeline', 'web/src/lib/media-conversion']:
        if (ROOT / directory).exists():
            for path in sorted((ROOT / directory).rglob('*.ts*')):
                add('internal.' + path.relative_to(ROOT).as_posix(), path.relative_to(ROOT).as_posix(), 'editor-internal')
    return {'schemaVersion': 1, 'baseline': BASELINE, 'status': 'inventory-pending-migration',
            'coverageNote': '静态枚举入口；各编辑器内部行为仍需按真实 UI 和 MCP 输出逐项验收。',
            'features': sorted(entries.values(), key=lambda item: item['featureId'])}


if __name__ == '__main__':
    inventory = collect()
    (ROOT / 'docs/canvas-capabilities.json').write_text(json.dumps(inventory, ensure_ascii=False, indent=2) + '\n', encoding='utf-8')
    print(json.dumps({'features': len(inventory['features']), 'status': inventory['status']}))
