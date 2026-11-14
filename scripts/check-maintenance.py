"""Check formatting, JavaScript syntax and explicitly versioned shared copies."""
from pathlib import Path
import hashlib, json, subprocess, sys
root=Path(__file__).resolve().parents[1]
files=subprocess.check_output(['git','ls-files','--cached','--others','--exclude-standard'],cwd=root,text=True).splitlines()
go=[str(root/name) for name in files if name.endswith('.go')]
if go:
 result=subprocess.run(['gofmt','-l',*go],capture_output=True,text=True)
 if result.returncode or result.stdout.strip():
  print('Go formatting check failed'); print(result.stdout); raise SystemExit(1)
for name in files:
 if name.endswith(('.js','.mjs','.cjs')) and not name.startswith(('sdk/third_party/','legacy/')):
  subprocess.run(['node','--check',str(root/name)],check=True)
registry=root/'docs/shared-components.json'
if registry.exists():
 data=json.loads(registry.read_text(encoding='utf-8'))
 for component in data['components']:
  source=(root/component['source']).read_bytes().replace(b'\r\n',b'\n')
  digest=hashlib.sha256(source).hexdigest()
  if '--write' in sys.argv:
   component['sha256']=digest
   for mirror in component.get('mirrors',[]): (root/mirror).write_bytes(source)
  elif digest!=component['sha256']:
   raise SystemExit('Shared source changed: update its version and run scripts/check-maintenance.py --write')
  for mirror in component.get('mirrors',[]):
   if (root/mirror).read_bytes().replace(b'\r\n',b'\n')!=source: raise SystemExit('Shared mirror drift: '+mirror)
 if '--write' in sys.argv: registry.write_text(json.dumps(data,ensure_ascii=False,indent=2)+'\n',encoding='utf-8')
print('Maintenance contracts passed')
