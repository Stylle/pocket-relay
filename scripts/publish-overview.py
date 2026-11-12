"""Publish only the public image's deployment guide; never print credentials."""
import json
import os
from pathlib import Path
import sys
import urllib.error
import urllib.request

text = (Path(__file__).resolve().parents[1] / 'docs/dockerhub-description.md').read_text(encoding='utf-8')
if len(text.encode()) > 25000:
    raise SystemExit('Overview exceeds Docker Hub size limit')
if '--check' in sys.argv:
    print('Public overview size validated')
    raise SystemExit(0)

def request(url, body=None, token=None, method=None):
    headers = {'Content-Type': 'application/json'}
    if token:
        headers['Authorization'] = 'Bearer ' + token
    req = urllib.request.Request(url, data=json.dumps(body).encode() if body is not None else None, headers=headers, method=method)
    with urllib.request.urlopen(req, timeout=45) as response:
        return json.load(response)

try:
    secret = os.environ['HUB_TOKEN']
    auth = request('https://hub.docker.com/v2/auth/token', {'identifier': 'stylle', 'secret': secret})
    secret = None
    token = auth['access_token']
    url = 'https://hub.docker.com/v2/repositories/stylle/pocket-relay/'
    request(url, {'is_private': False, 'description': 'Lightweight self-hosted file and message relay with TOTP, WebDAV, SMB and encrypted backups.', 'full_description': text}, token, 'PATCH')
    saved = request(url, token=token)
    if saved.get('is_private') or saved.get('full_description') != text:
        raise RuntimeError('Verification failed')
    print('Public Docker Hub overview published and verified')
except Exception as error:
    status = f' HTTP {error.code}' if isinstance(error, urllib.error.HTTPError) else ''
    print('Overview publication failed: ' + type(error).__name__ + status)
    raise SystemExit(1)
