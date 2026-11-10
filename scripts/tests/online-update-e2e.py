#!/usr/bin/env python3
"""Real local management server + Go client + fixed bootstrap. Never uses NAS credentials."""
import hashlib, http.client, http.cookiejar, json, os, pathlib, secrets, shutil, socket, subprocess, tempfile, time, urllib.request, urllib.error
ROOT = pathlib.Path(__file__).resolve().parents[2]
BASE = pathlib.Path(tempfile.mkdtemp(prefix='seekbox-update-e2e-', dir='/dev/shm'))
SUMMARY = ROOT / 'build/online-update-e2e.json'
summary = {'isolated': True, 'root': str(BASE), 'checks': []}

def command(args, **kwargs):
    result = subprocess.run([str(x) for x in args], check=True, capture_output=True, **kwargs)
    return result.stdout

def free_port():
    with socket.socket() as s: s.bind(('127.0.0.1',0)); return s.getsockname()[1]

def wait_for(fn, label, timeout=90):
    until = time.monotonic()+timeout
    last = None
    while time.monotonic()<until:
        try:
            value=fn()
            if value: return value
        except (OSError, ValueError, KeyError, urllib.error.URLError, http.client.HTTPException) as e: last=type(e).__name__
        time.sleep(.35)
    raise RuntimeError(f'{label} timeout (last={last})')

class UnixHTTP(http.client.HTTPConnection):
    def __init__(self, name): super().__init__('localhost',timeout=5); self.name=name
    def connect(self): self.sock=socket.socket(socket.AF_UNIX);self.sock.settimeout(5);self.sock.connect(str(self.name))

app=BASE/'app'; var=BASE/'var'; app.mkdir();var.mkdir()
for folder in ['bin','web','auth']: (app/folder).mkdir()
shutil.copy2('/tmp/seekbox-update-v160',app/'bin/seekbox')
shutil.copy2(ROOT/'app/bin/gateway.py',app/'bin/gateway.py')
shutil.copy2(ROOT/'app/bin/update-helper.py',app/'bin/update-helper.py')
shutil.copy2(ROOT/'cmd/main',app/'bin/seekbox-bootstrap.sh')
shutil.copytree(ROOT/'build/e2e-bootstrap-web',app/'web',dirs_exist_ok=True)
(app/'web/version.json').write_text('{"version":"1.6.0"}')
for name in ['login.cjs','cookies.cjs']: shutil.copy2(ROOT/'app/auth'/name,app/'auth'/name)
backend_port=free_port(); manager_port=free_port(); origin=f'http://127.0.0.1:{manager_port}'
server=ROOT/'management-center/build/server'
password=secrets.token_urlsafe(24)
password_hash=command([server,'-hash-password'],input=password.encode())
(BASE/'admin-hash').write_bytes(password_hash)
key=BASE/'test-release.pem'
command(['go','run',ROOT/'scripts/release/main.go','-generate-key','-key-id','local-e2e','-private-key',key],cwd=ROOT)
public=pathlib.Path(str(key)+'.public.json')
jar=http.cookiejar.CookieJar(); client=urllib.request.build_opener(urllib.request.HTTPCookieProcessor(jar))

def manager(method, route, body=None, raw=None, content_type=None):
    data=raw if raw is not None else (json.dumps(body).encode() if body is not None else None)
    headers={'Origin':origin}
    if data is not None: headers['Content-Type']=content_type or 'application/json'
    req=urllib.request.Request(origin+route,data=data,headers=headers,method=method)
    with client.open(req,timeout=40) as response:
        return json.loads(response.read())

def app_api(method,route,body=None,user='1000'):
    connection=UnixHTTP(app/'app.sock')
    headers={'X-Trim-Userid':user}
    if body is not None: headers.update({'Content-Type':'application/json','X-Seekbox-Action':'1'})
    try:
        connection.request(method,'/app/fn-seekbox/api'+route,json.dumps(body) if body is not None else None,headers)
        response=connection.getresponse(); data=json.loads(response.read())
        if response.status>=400: raise ValueError(f'app HTTP {response.status}: {data.get("code", "error")}')
        return data
    finally: connection.close()

def multipart(files):
    boundary='seekbox-'+secrets.token_hex(16); parts=[]
    for name, filename in files.items():
        p=pathlib.Path(filename)
        parts.extend([f'--{boundary}\r\nContent-Disposition: form-data; name="{name}"; filename="{p.name}"\r\nContent-Type: application/octet-stream\r\n\r\n'.encode(),p.read_bytes(),b'\r\n'])
    parts.append(f'--{boundary}--\r\n'.encode())
    return b''.join(parts),'multipart/form-data; boundary='+boundary

def release(version,binary):
    package=BASE/f'package-{version}'; (package/'app/bin').mkdir(parents=True)
    shutil.copy2(binary,package/'app/bin/seekbox'); shutil.copy2(ROOT/'app/bin/gateway.py',package/'app/bin/gateway.py')
    shutil.copytree(ROOT/'frontend/dist',package/'app/web')
    (package/'app/web/version.json').write_text(json.dumps({'version':version}))
    (package/'app/auth').mkdir()
    for f in ['login.cjs','cookies.cjs']: shutil.copy2(ROOT/'app/auth'/f,package/'app/auth'/f)
    out=BASE/f'release-{version}'
    command(['go','run',ROOT/'scripts/release/main.go','-root',package,'-version',version,'-arch','amd64','-key-id','local-e2e','-private-key',key,'-out',out,'-notes',f'本地隔离验收 {version}'],cwd=ROOT)
    raw,content_type=multipart({'manifest':out/'release.json','artifact':out/f'fn-seekbox-{version}-amd64.tar.gz'})
    result=manager('POST','/seekbox-config/api/releases',raw=raw,content_type=content_type)
    record=result['data']; record=record.get('release',record); rid=record['id']
    assert not manager('GET',f'/api/client/releases/latest?app=fn-seekbox&channel=stable&arch=amd64&version={"1.6.0" if version=="1.6.1" else "1.6.1"}')['data']['available']
    manager('POST',f'/seekbox-config/api/releases/{rid}/publish',{})
    return rid

env={**os.environ,'TRIM_APPDEST':str(app),'TRIM_PKGVAR':str(var),'TRIM_TEMP_LOGFILE':str(BASE/'app-error.log'),'SEEKBOX_BACKEND_PORT':str(backend_port),'SEEKBOX_UPDATE_BASE_URL':origin,'SEEKBOX_CENTER_URL':origin,'SEEKBOX_UPDATE_PUBLIC_KEYS_FILE':str(public),'SEEKBOX_UPDATE_ADMIN_IDS':'1000','SEEKBOX_UPDATE_ALLOW_LOOPBACK_HTTP':'1','SEEKBOX_UPDATE_TEST_HEALTH_TIMEOUT':'6','ASYNC_PLUGIN_ENABLED':'false','ENABLED_PLUGINS':'none','CACHE_ENABLED':'false','NO_PROXY':'127.0.0.1,localhost'}
(var/'runtime').mkdir();(var/'runtime/settings.json').write_text('{"auto_enabled":false}')
(var/'data').mkdir(); sentinel=var/'data/retained.txt'; sentinel.write_text('isolated-account-and-plan-data-must-survive')
process=None
log=(BASE/'manager.log').open('wb')
try:
    process=subprocess.Popen([str(server),f'-addr=127.0.0.1:{manager_port}',f'-db={BASE}/config.db','-seed=',f'-web={ROOT}/management-center/frontend/dist',f'-admin-password-hash-file={BASE}/admin-hash',f'-origin={origin}','-allow-insecure-loopback',f'-release-public-keys-file={public}'],stdout=log,stderr=log)
    wait_for(lambda: urllib.request.urlopen(origin+'/healthz',timeout=2).status==200,'manager startup')
    manager('POST','/seekbox-config/api/login',{'password':password})
    rid=release('1.6.1','/tmp/seekbox-update-v161');summary['checks'].append('signed upload -> draft hidden -> publish')
    command(['bash',app/'bin/seekbox-bootstrap.sh','start'],env=env)
    assert app_api('GET','/health')['version']=='1.6.0'
    assert not app_api('GET','/update/status',user='1001')['can_manage']
    app_api('PUT','/cloud/center',{'daily_enabled':False,'reporting_enabled':True})
    wait_for(lambda: app_api('GET','/cloud/center').get('connected'),'isolated client registration')
    keyhash=hashlib.sha256((var/'data/cloud/credentials.key').read_bytes()).hexdigest()
    app_api('POST','/update/install',{})
    journal=var/'runtime/update-state.json'
    wait_for(lambda: journal.exists() and json.loads(journal.read_text()).get('phase')=='committed' and app_api('GET','/health')['version']=='1.6.1','real upgrade',150)
    summary['checks'].append('real Go 1.6.0 -> 1.6.1 through Unix gateway')
    def recorded(status,release_id):
        data=manager('GET','/seekbox-config/api/releases')['data']
        return next((x for x in data.get('recent_reports',[]) if x.get('status')==status and x.get('release_id')==release_id),None)
    wait_for(lambda:recorded('success',rid),'success report',120)
    summary['checks'].append('durable success report received by real management center')
    # /bin/false is a valid ELF that deliberately fails startup, signed only by the throwaway test key.
    bad_id=release('1.6.2','/bin/false')
    app_api('POST','/update/install',{})
    wait_for(lambda: json.loads(journal.read_text()).get('version')=='1.6.2' and json.loads(journal.read_text()).get('phase')=='rolled_back' and app_api('GET','/health')['version']=='1.6.1','bad release rollback',180)
    summary['checks'].append('validly signed non-starting ELF -> rollback to 1.6.1')
    wait_for(lambda:recorded('rolled_back',bad_id),'rollback report',120)
    summary['checks'].append('rollback report received by real management center')
    assert sentinel.read_text()=='isolated-account-and-plan-data-must-survive'
    assert hashlib.sha256((var/'data/cloud/credentials.key').read_bytes()).hexdigest()==keyhash
    assert app_api('GET','/cloud/center')['connected']
    summary['checks'].append('user data, credential key and registered identity preserved')
    manager('POST',f'/seekbox-config/api/releases/{bad_id}/withdraw',{})
    assert not manager('GET','/api/client/releases/latest?app=fn-seekbox&channel=stable&arch=amd64&version=1.6.1')['data']['available']
    summary['checks'].append('withdraw immediately removes release from update feed')
    summary['passed']=True
except Exception as exc:
    summary['passed']=False;summary['error']=str(exc);raise
finally:
    try: command(['bash',app/'bin/seekbox-bootstrap.sh','stop'],env=env)
    except Exception: pass
    if process:
        process.terminate()
        try: process.wait(timeout=15)
        except subprocess.TimeoutExpired: process.kill();process.wait()
    log.close()
    # Local test private signing key and admin secret are never retained in artifacts.
    key.unlink(missing_ok=True);(BASE/'admin-hash').unlink(missing_ok=True)
    SUMMARY.parent.mkdir(parents=True,exist_ok=True);SUMMARY.write_text(json.dumps(summary,ensure_ascii=False,indent=2)+'\n')
    print(json.dumps(summary,ensure_ascii=False))
