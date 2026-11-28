#!/usr/bin/env python3
"""Disposable Ubuntu runner only. Never run on a production VPS."""
import base64
import hashlib
import http.cookiejar
import json
import os
import re
import secrets
import shlex
import socketserver
import threading
import subprocess
import time
import urllib.error
import urllib.parse
import urllib.request

assert os.environ.get("GITHUB_ACTIONS") == "true"
assert os.environ.get("RUNNER_ENVIRONMENT") == "github-hosted"
PANEL = "http://127.0.0.1:8443"
MCP = "http://127.0.0.1:18443"
HOST = "mcp.example.test"
ORIGIN = "https://" + HOST
CALLBACK = "https://chatgpt.com/connector_platform_oauth_redirect"
USER = "ogpmcp"

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None

opener = urllib.request.build_opener(NoRedirect(), urllib.request.HTTPCookieProcessor(http.cookiejar.CookieJar()))

def request(base, path, data=None, token=None, expected=200):
    headers = {"Host": HOST} if base == MCP else {}
    if token:
        headers["Authorization"] = "Bearer " + token
    if isinstance(data, dict):
        data = urllib.parse.urlencode(data,doseq=True).encode()
        headers["Content-Type"] = "application/x-www-form-urlencoded"
    elif isinstance(data, bytes):
        headers["Content-Type"] = "application/json"
        headers["Accept"] = "application/json, text/event-stream"
    req = urllib.request.Request(base + path, data=data, headers=headers)
    try:
        response = opener.open(req, timeout=150)
    except urllib.error.HTTPError as error:
        response = error
    with response:
        status = response.code
        body = response.read().decode()
        if status != expected:
            # Do not print OAuth responses or token-bearing redirects.
            raise AssertionError(f"{path.split('?')[0]}: HTTP {status}, expected {expected}")
        return body, response.headers

def sudo(*args, input=None):
    return subprocess.check_output(["sudo", *args], input=input, text=True).strip()

def wait_health(base):
    for _ in range(30):
        try:
            request(base, "/health")
            return
        except (OSError, AssertionError):
            time.sleep(1)
    raise AssertionError("service did not become healthy")

password = sudo("sed", "-n", "s/^OGP_ADMIN_PASSWORD=//p", "/etc/open-go-panel/open-go-panel.env")
request(PANEL, "/login", {"username":"admin","password":password}, expected=303)
page, _ = request(PANEL, "/")
csrf = re.search(r'name="ogp_csrf" value="([^"]+)"', page).group(1)

def post(path, fields=None):
    request(PANEL, path, {"ogp_csrf":csrf, **(fields or {})}, expected=303)

def app_ids():
    page, _ = request(PANEL, "/apps")
    return set(re.findall(r'href="/apps/([0-9]+)"', page))

post("/settings/chatgpt", {"domain":HOST})
settings, _ = request(PANEL, "/settings/chatgpt")
assert 'value="' + HOST + '"' in settings, "domain not saved"
post("/users", {"username":USER, "password":"SmokePass123!"})
before = app_ids()
post("/apps", {"user":USER,"name":"yo4","type":"static"})
a, = app_ids() - before
before = app_ids()
post("/apps", {"user":USER,"name":"svg4","type":"static"})
b, = app_ids() - before
root_a = f"/home/{USER}/apps/yo4"
root_b = f"/home/{USER}/apps/svg4"
sudo("-u",USER,"sh","-c",f"printf 'secret sibling\\n' > {root_b}/secret.txt; printf 'original\\n' > {root_a}/demo.txt; ln -s ../svg4/secret.txt {root_a}/escape-relative; ln -s {root_b}/secret.txt {root_a}/escape-absolute")
sudo("sh","-c",f"printf 'read only\\n' > {root_a}/readonly.txt; chmod 0444 {root_a}/readonly.txt")

def authorize(app_id, enable=True):
    if enable: post(f"/apps/{app_id}/chatgpt/enable")
    page, _ = request(PANEL, f"/apps/{app_id}")
    assert "Awaiting authorization" in page
    resource = re.search(r'id="mcp-plugin-url" readonly value="([^"]+)"',page).group(1)
    connection = resource.rsplit("/",1)[1]
    path = "/mcp/" + connection
    oauth = "/oauth/" + connection
    _, headers = request(MCP, path, b'{"jsonrpc":"2.0","id":1,"method":"tools/list"}', expected=401)
    assert "resource_metadata" in headers["WWW-Authenticate"]
    metadata = json.loads(request(MCP, "/.well-known/oauth-protected-resource"+path)[0])
    assert metadata["resource"] == resource
    registration = json.loads(request(MCP,oauth+"/register",json.dumps({"redirect_uris":[CALLBACK],"token_endpoint_auth_method":"none","grant_types":["authorization_code","refresh_token"]}).encode(),expected=201)[0])
    client=registration["client_id"]
    verifier=secrets.token_urlsafe(48)
    challenge=base64.urlsafe_b64encode(hashlib.sha256(verifier.encode()).digest()).decode().rstrip("=")
    auth_query=urllib.parse.urlencode({"response_type":"code","client_id":client,"redirect_uri":CALLBACK,"resource":resource,"code_challenge_method":"S256","code_challenge":challenge,"state":"mcp-smoke","scope":"project"})
    auth_page,_=request(MCP,oauth+"/authorize?"+auth_query)
    poll=re.search(r'name="poll" value="([^"]+)"',auth_page).group(1)
    req_id=re.search(r'Request ID: <code>([^<]+)</code>',auth_page).group(1)
    request(MCP,oauth+"/continue",{"poll":poll})
    page,_=request(PANEL,f"/apps/{app_id}")
    assert "Authorization request received" in page and req_id in page
    post(f"/apps/{app_id}/chatgpt/decide",{"request_id":req_id,"decision":"approve"})
    page,_=request(PANEL,f"/apps/{app_id}")
    assert "Connected " not in page
    _,headers=request(MCP,oauth+"/continue",{"poll":poll},expected=303)
    callback=urllib.parse.urlparse(headers["Location"])
    args=urllib.parse.parse_qs(callback.query)
    assert callback.netloc=="chatgpt.com" and args["iss"]==[ORIGIN+oauth]
    tokens=json.loads(request(MCP,oauth+"/token",{"grant_type":"authorization_code","client_id":client,"redirect_uri":CALLBACK,"resource":resource,"code":args["code"][0],"code_verifier":verifier})[0])
    page,_=request(PANEL,f"/apps/{app_id}")
    assert "Connected " in page
    return path,oauth,resource,client,tokens

a_conn=authorize(a)
b_conn=authorize(b)
def rpc(conn,method,params=None,expected=200):
    message={"jsonrpc":"2.0","id":1,"method":method}
    if params is not None:message["params"]=params
    body,_=request(MCP,conn[0],json.dumps(message).encode(),conn[4]["access_token"],expected)
    return json.loads(body) if expected==200 else None

def call(conn,name,args,failed=False):
    response=rpc(conn,"tools/call",{"name":name,"arguments":args})
    assert "result" in response, f"{name}: RPC error"
    result=response["result"]
    if result["isError"] != failed:
        detail=result.get("structuredContent",{})
        # Only test-generated file contents/commands appear in this runner.
        raise AssertionError(f"{name}: expected failure={failed}; got {detail}")
    return result["structuredContent"]

# Ordinary host permissions, not the former network namespace.
class DatabaseProbe(socketserver.BaseRequestHandler):
    def handle(self):
        self.request.sendall(str(self.server.server_address[1]).encode())

def collect(conn, session_id, timeout=20):
    end=time.monotonic()+timeout
    offset=0
    data=b""
    while time.monotonic()<end:
        result=call(conn,"terminal_read",{"session_id":session_id,"offset":offset})
        assert not result["truncated"], "unexpected output loss"
        data+=base64.b64decode(result["output_base64"]) if "output_base64" in result else result["output"].encode()
        offset=result["next_offset"]
        if result["status"]=="exited" and offset==result["end_offset"]:
            return data.decode(errors="replace"),result["exit_code"]
        time.sleep(.15)
    raise AssertionError(f"terminal did not finish; test output: {data[-4096:]!r}")

def command(conn, text, timeout=20):
    result=call(conn,"terminal_start",{"command":text})
    out,code=collect(conn,result["session_id"],timeout)
    assert code==0, f"terminal command failed: {out}"
    return out

endpoint=socketserver.ThreadingTCPServer(("127.0.0.1",0),DatabaseProbe)
threading.Thread(target=endpoint.serve_forever,daemon=True).start()
try:
    port=endpoint.server_address[1]
    source=f"import socket; s=socket.create_connection(('127.0.0.1',{port}),2); print(s.recv(32).decode()); s.close()"
    assert str(port) in command(a_conn,"python3 -c "+shlex.quote(source))
finally:
    endpoint.shutdown()
    endpoint.server_close()

assert len(rpc(a_conn,"tools/list")["result"]["tools"])==17
assert rpc(a_conn,"initialize",{"protocolVersion":"2025-11-25"})["result"]["protocolVersion"]=="2025-11-25"
data=call(a_conn,"read_file",{"path":"demo.txt"})
assert data["content"]=="original\n"
call(a_conn,"edit_file",{"path":"demo.txt","version":data["version"],"content":"edited\n"})
assert sudo("-u",USER,"cat",root_a+"/demo.txt")=="edited"
data=call(a_conn,"read_file",{"path":"demo.txt"})
sudo("-u",USER,"sh","-c",f"printf manual > {root_a}/demo.txt")
call(a_conn,"edit_file",{"path":"demo.txt","version":data["version"],"content":"lost"},failed=True)
assert sudo("-u",USER,"cat",root_a+"/demo.txt")=="manual"
data=call(a_conn,"read_file",{"path":"readonly.txt"})
call(a_conn,"edit_file",{"path":"readonly.txt","version":data["version"],"content":"denied"},failed=True)
call(a_conn,"create_file",{"path":"created.txt","content":"find this string\n"})
assert sudo("stat","-c","%U",root_a+"/created.txt")==USER
call(a_conn,"create_file",{"path":"created.txt","content":"no overwrite"},failed=True)
assert call(a_conn,"search_files",{"query":"find this string"})["matches"][0]["path"]=="created.txt"
call(a_conn,"create_directory",{"path":"empty"})
call(a_conn,"delete_path",{"path":"empty"})
for target in ["../svg4/secret.txt",root_b+"/secret.txt","escape-relative","escape-absolute"]:
    call(a_conn,"read_file",{"path":target},failed=True)
assert sudo("-u",USER,"cat",root_b+"/secret.txt")=="secret sibling"
assert call(b_conn,"read_file",{"path":"secret.txt"})["content"]=="secret sibling\n"

# MCP returns a real native image content block, not a JSON/base64 text tool.
import struct
import zlib
def png_chunk(kind, data):
    return struct.pack(">I",len(data))+kind+data+struct.pack(">I",zlib.crc32(kind+data)&0xffffffff)
png=b"\x89PNG\r\n\x1a\n"+png_chunk(b"IHDR",struct.pack(">IIBBBBB",2,1,8,2,0,0,0))+png_chunk(b"IDAT",zlib.compress(b"\x00\xff\x00\x00\x00\xff\x00"))+png_chunk(b"IEND",b"")
sudo("-u",USER,"python3","-c","import base64,sys;open(sys.argv[1],'wb').write(base64.b64decode(sys.argv[2]))",root_a+"/shot.png",base64.b64encode(png).decode())
response=rpc(a_conn,"tools/call",{"name":"view_image","arguments":{"path":"shot.png"}})["result"]
assert not response["isError"] and len(response["content"])==1
image=response["content"][0]
assert image["type"]=="image" and image["mimeType"]=="image/png" and base64.b64decode(image["data"])==png
for bad in ("demo.txt","../svg4/secret.txt","escape-relative"):
    call(a_conn,"view_image",{"path":bad},failed=True)
call(b_conn,"view_image",{"path":"shot.png"},failed=True)

webp=base64.b64decode("UklGRiAAAABXRUJQVlA4TBQAAAAvA0AAEA8QEfMfQiGAAChoIvofDw==")
for filename in ("icon.webp","icon-2.webp"):
    sudo("-u",USER,"python3","-c","import base64,sys;open(sys.argv[1],'wb').write(base64.b64decode(sys.argv[2]))",root_a+"/"+filename,base64.b64encode(webp).decode())
response=rpc(a_conn,"tools/call",{"name":"view_image","arguments":{"path":"icon.webp","background":"#ffffff","max_width":2}})["result"]
image=response["content"][0]
assert not response["isError"] and image["type"]=="image" and image["mimeType"]=="image/png"
assert struct.unpack(">II",base64.b64decode(image["data"])[16:24])==(2,1)
response=rpc(a_conn,"tools/call",{"name":"view_images","arguments":{"paths":["icon.webp","missing.webp","icon-2.webp","../svg4/secret.txt"],"columns":2,"max_width":300,"max_height":300}})["result"]
assert not response["isError"] and len(response["content"])==1
image=response["content"][0]
assert image["type"]=="image" and image["mimeType"]=="image/png"
assert struct.unpack(">II",base64.b64decode(image["data"])[16:24])==(632,724)
call(b_conn,"view_image",{"path":"icon.webp"},failed=True)

# Shell failure is a result, not a protocol/tool failure.
response=rpc(a_conn,"tools/call",{"name":"run_command","arguments":{"command":"ogp_test_nonexistent_command_123","timeout_seconds":5}})["result"]
assert not response["isError"] and response["content"]==[]
result=response["structuredContent"]
assert result["status"]=="exited" and result["exit_code"]==127 and "not found" in result["output"]
assert "stdout" not in result and "stderr" not in result and "output_base64" not in result

response=rpc(a_conn,"tools/call",{"name":"run_command","arguments":{"command":"printf 'compact-unique-marker'","timeout_seconds":5,"response_format":"text"}})["result"]
assert "structuredContent" not in response and json.loads(response["content"][0]["text"])["output"]=="compact-unique-marker"

result=call(a_conn,"run_command",{"command":"python3 -c 'import sys; sys.stdout.write(\"x\"*100000)'","max_output_bytes":4096,"timeout_seconds":5})
assert len(result["output"])==4096 and result["next_offset"]==4096 and result["has_more"]
parts=[result["output"]]
offset=result["next_offset"]
while True:
    chunk=call(a_conn,"terminal_read",{"session_id":result["session_id"],"offset":offset,"max_output_bytes":16384})
    assert not chunk["truncated"] and len(chunk["output"].encode())<=16384
    parts.append(chunk["output"])
    offset=chunk["next_offset"]
    if chunk["status"]=="exited" and not chunk["has_more"]:break
assert "".join(parts)=="x"*100000
result=call(a_conn,"run_command",{"command":"printf abc","response_format":"legacy","timeout_seconds":5})
assert result["stdout"]==result["output"]=="abc" and base64.b64decode(result["output_base64"])==b"abc"


out=command(a_conn,"id -u; pwd; printf ok > command.txt")
assert sudo("id","-u",USER) in out and root_a in out
assert sudo("stat","-c","%U",root_a+"/command.txt")==USER
assert "secret sibling" in command(a_conn,"cat "+shlex.quote(root_b+"/secret.txt"))
# Arbitrary host paths and network access now follow Linux permissions.
result=call(a_conn,"run_command",{"command":"sleep 3; printf 'later-output\\n'","timeout_seconds":1})
assert result["status"]!="exited", "run_command killed a long process"
out,code=collect(a_conn,result["session_id"])
assert code==0 and "later-output" in out

# Actual interactive shell, repeated MCP requests, and isolated grant ownership.
interactive=call(a_conn,"terminal_start",{})["session_id"]
call(a_conn,"terminal_resize",{"session_id":interactive,"rows":32,"cols":110})
call(b_conn,"terminal_read",{"session_id":interactive},failed=True)
call(a_conn,"terminal_write",{"session_id":interactive,"input":"printf '%s%s\\n' 'interactive-' 'marker'; exit 0\n"})
out,code=collect(a_conn,interactive)
assert code==0 and "interactive-marker" in out
assert any(s["session_id"]==interactive for s in call(a_conn,"terminal_list",{})["sessions"])

# Download a previously unknown executable from a test HTTP endpoint.
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
class ToolDownload(BaseHTTPRequestHandler):
    def do_GET(self):
        body=b"#!/bin/sh\nprintf 'downloaded-tool-ok\\n'\n"
        self.send_response(200); self.send_header("Content-Length",str(len(body))); self.end_headers(); self.wfile.write(body)
    def log_message(self,*args): pass
download=ThreadingHTTPServer(("127.0.0.1",0),ToolDownload)
threading.Thread(target=download.serve_forever,daemon=True).start()
try:
    url=f"http://127.0.0.1:{download.server_address[1]}/unknown-tool"
    assert "downloaded-tool-ok" in command(a_conn,"mkdir -p \"$HOME/.local/bin\"; curl -fsS "+shlex.quote(url)+" -o \"$HOME/.local/bin/ogp-test-tool\" && chmod +x \"$HOME/.local/bin/ogp-test-tool\" && \"$HOME/.local/bin/ogp-test-tool\"")
finally:
    download.shutdown();download.server_close()

# Real outbound HTTPS, independent of the runner-local test endpoint.
assert "Copyright" in command(a_conn,"curl -fsSL --retry 2 --connect-timeout 5 --max-time 20 https://raw.githubusercontent.com/golang/go/master/LICENSE",timeout=75)

# Explicit stop also terminates managed children.
marker="mcp-child-"+secrets.token_hex(8)
session=call(a_conn,"terminal_start",{"command":f"sh -c 'sleep 60; echo {marker}' & wait"})["session_id"]
call(a_conn,"terminal_stop",{"session_id":session})
assert call(a_conn,"terminal_status",{"session_id":session})["status"]=="exited"
assert subprocess.run(["sudo","pgrep","-u",USER,"-f",marker],capture_output=True).returncode==1, "orphan process"
# Large projects must not be rejected before the requested operation starts.
# Use one wide directory to also exercise the streaming directory traversal.
sudo("-u",USER,"python3","-c", "import os,sys; d=sys.argv[1]; os.mkdir(d); [open(os.path.join(d,str(i)), 'x').close() for i in range(200001)]",root_a+"/large")
try:
    assert call(a_conn,"read_file",{"path":"demo.txt"})["content"]=="manual"
    assert call(a_conn,"list_directory",{"path":"large"})["truncated"]
    assert root_a in command(a_conn,"pwd && id -u")
finally:
    sudo("-u",USER,"rm","-rf",root_a+"/large")
# Same-UID hardlinks remain blocked; unrelated file tools still work.
sudo("-u",USER,"ln",root_b+"/secret.txt",root_a+"/hardlink")
try:
    call(a_conn,"read_file",{"path":"hardlink"},failed=True)
    call(a_conn,"edit_file",{"path":"hardlink","content":"bad","version":hashlib.sha256(b"secret sibling\\n").hexdigest()},failed=True)
    call(a_conn,"delete_path",{"path":"hardlink"},failed=True)
    assert call(a_conn,"read_file",{"path":"demo.txt"})["content"]=="manual"
    assert sudo("-u",USER,"cat",root_b+"/secret.txt")=="secret sibling"
finally:
    sudo("-u",USER,"rm",root_a+"/hardlink")
live=call(a_conn,"terminal_start",{"command":"sleep 3; printf 'survived-panel-close\\n'"})["session_id"]
sudo("systemctl","stop","open-go-panel.service")
assert len(rpc(a_conn,"tools/list")["result"]["tools"])==17
out,code=collect(a_conn,live)
assert code==0 and "survived-panel-close" in out
sudo("systemctl","start","open-go-panel.service")
wait_health(PANEL)
request(PANEL, "/login", {"username":"admin","password":password}, expected=303)
page,_=request(PANEL,"/")
csrf=re.search(r'name="ogp_csrf" value="([^"]+)"',page).group(1)
sudo("systemctl","restart","open-go-panel-mcp.service")
wait_health(MCP)
assert len(rpc(a_conn,"tools/list")["result"]["tools"])==17
sudo("systemctl","is-enabled","open-go-panel-mcp.service")
# Grant policy changes revoke both OAuth tokens and all associated sessions.
old=a_conn
long=call(a_conn,"terminal_start",{"command":"sleep 120"})["session_id"]
post(f"/apps/{a}/chatgpt/terminal-mode",{"mode":"root","confirm":"1"})
rpc(old,"tools/list",expected=401)
request(MCP,old[1]+"/token",{"grant_type":"refresh_token","client_id":old[3],"resource":old[2],"refresh_token":old[4]["refresh_token"]},expected=400)
assert subprocess.run(["sudo","systemctl","is-active","--quiet",f"ogp-mcp-terminal-{long}.service"]).returncode!=0
a_conn=authorize(a,enable=False)
assert command(a_conn,"id -u").strip()=="0"
# Package installation is tested only on the disposable hosted runner.
assert "Hello, world!" in command(a_conn,"DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends hello && /usr/bin/hello",timeout=120)
# A root terminal must not change the file-tool identity.
call(a_conn,"create_file",{"path":"still-user.txt","content":"user"})
assert sudo("stat","-c","%U",root_a+"/still-user.txt")==USER
post(f"/apps/{a}/chatgpt/terminal-mode",{"mode":"sudo","confirm":"1"})
rpc(a_conn,"tools/list",expected=401)
a_conn=authorize(a,enable=False)
# sudo policy remains the server owner's explicit decision.
sudo("tee","/etc/sudoers.d/ogpmcp-test",input=f"{USER} ALL=(root) NOPASSWD: /usr/bin/id -u\n")
sudo("chmod","0440","/etc/sudoers.d/ogpmcp-test")
try:
    assert command(a_conn,"sudo -n /usr/bin/id -u").strip()=="0"
    assert command(a_conn,"id -u").strip()==sudo("id","-u",USER)
finally:
    sudo("rm","-f","/etc/sudoers.d/ogpmcp-test")
long=call(a_conn,"terminal_start",{"command":"sleep 120"})["session_id"]
post(f"/apps/{a}/chatgpt/revoke")
assert subprocess.run(["sudo","systemctl","is-active","--quiet",f"ogp-mcp-terminal-{long}.service"]).returncode!=0
rpc(a_conn,"tools/list",expected=401)
assert len(rpc(b_conn,"tools/list")["result"]["tools"])==17
request(MCP,a_conn[1]+"/token",{"grant_type":"refresh_token","client_id":a_conn[3],"resource":a_conn[2],"refresh_token":a_conn[4]["refresh_token"]},expected=404)
request(MCP,"/admin",expected=404)
print("MCP OAuth, file isolation, host PTY, downloads, sudo/root, policy rotation and revocation passed")


# App dependency smoke on the same disposable systemd runner.
# Actual compiled Go binary and an Air-shaped supervisor which keeps running
# without a listener. No production service or real user application touched.
import pathlib
import tempfile
import socket
before=app_ids()
post("/apps",{"user":USER,"name":"dependency-test","type":"go"})
dep_id,=app_ids()-before
dep_root=f"/home/{USER}/apps/dependency-test"
app_unit=f"open-go-panel-app-{dep_id}.service"
dropdir=f"/etc/systemd/system/{app_unit}.d"
dropin=dropdir+"/50-ogp-dependencies.conf"
custom=dropdir+"/90-test-custom.conf"
ready="ogp-test-ready.service"
failure="ogp-test-failure.service"
cycle="ogp-test-cycle.service"
marker="/run/ogp-dependency-ready"
def unit_file(name,body):
    sudo("tee","/etc/systemd/system/"+name,input=body)
def dep_save(units,modes,expected=303):
    request(PANEL,f"/apps/{dep_id}/dependencies",{"ogp_csrf":csrf,"dependency_unit":units,"dependency_mode":modes},expected=expected)
def state_page():
    return request(PANEL,f"/apps/{dep_id}")[0]
def status_is(label):
    return re.search(r'class="runtime-state"><strong[^>]*>'+label+r'</strong>',state_page()) is not None
def service_settings(mode,command=""):
    post(f"/apps/{dep_id}/service",{"mode":"form","run_mode":mode,"command":command,"restart":"no","auto_start":"1","path":dep_root+":/usr/local/go/bin:/usr/bin:/bin"})
try:
    with tempfile.TemporaryDirectory() as tmp:
        source=pathlib.Path(tmp)/"main.go"
        source.write_text('''package main
import ("os";"net/http";"log")
func main(){
 if _,err:=os.Stat("/run/ogp-dependency-ready");err!=nil{log.Fatal(err)}
 http.HandleFunc("/",func(w http.ResponseWriter,r *http.Request){w.Write([]byte("ready"))})
 log.Fatal(http.ListenAndServe("127.0.0.1:"+os.Getenv("PORT"),nil))
}
''')
        binary=str(pathlib.Path(tmp)/"app")
        subprocess.run(["go","build","-o",binary,str(source)],check=True)
        sudo("install","-o",USER,"-g",USER,"-m","0755",binary,dep_root+"/.ogp-app")
    service_settings("go-binary")
    unit_file(ready,f"[Unit]\nDescription=Test readiness\n[Service]\nType=oneshot\nRemainAfterExit=yes\nExecStart=/usr/bin/touch {marker}\nExecStop=/usr/bin/rm -f {marker}\n")
    unit_file(failure,"[Unit]\nDescription=Test failure\n[Service]\nType=oneshot\nExecStart=/usr/bin/false\n")
    unit_file(cycle,f"[Unit]\nDescription=Test cycle\nAfter={app_unit}\n[Service]\nType=oneshot\nExecStart=/usr/bin/true\n")
    sudo("mkdir","-p",dropdir)
    sudo("tee",custom,input="[Service]\nEnvironment=OGP_DEP_TEST=1\n")
    sudo("systemctl","daemon-reload")
    page=state_page()
    assert "System service dependencies" in page and ready in page
    dep_save([ready,failure,ready],["required","optional","optional"])
    configured=sudo("cat",dropin)
    assert f"Requires={ready}" in configured and f"Wants={failure}" in configured and configured.count(ready)==2
    assert subprocess.run(["sudo","systemctl","is-active","--quiet",app_unit]).returncode!=0
    post(f"/apps/{dep_id}/start")
    for _ in range(40):
        if status_is("Running"):break
        time.sleep(.25)
    else: raise AssertionError("compiled app did not become ready after dependencies")
    assert sudo("test","-f",marker)==""
    started=sudo("systemctl","show","--property=ActiveEnterTimestampMonotonic","--value",app_unit)
    dep_save([ready,failure],["required","optional"])
    assert sudo("systemctl","show","--property=ActiveEnterTimestampMonotonic","--value",app_unit)==started,"save restarted app"
    service_settings("go-binary")
    assert sudo("cat",dropin)==configured and ready in state_page()
    for bad in ["ogp-does-not-exist.service",app_unit,cycle,"bad.service\nAfter=ssh.service"]:
        dep_save([bad],["required"],expected=400)
        assert sudo("cat",dropin)==configured, "invalid dependency overwrote valid config"
    post(f"/apps/{dep_id}/stop")
    dep_save([failure],["required"])
    request(PANEL,f"/apps/{dep_id}/start",{"ogp_csrf":csrf},expected=400)
    assert subprocess.run(["sudo","systemctl","is-active","--quiet",app_unit]).returncode!=0,"required failure did not block app"
    dep_save([],[])
    assert subprocess.run(["sudo","test","-e",dropin]).returncode!=0
    assert "OGP_DEP_TEST=1" in sudo("cat",custom)
    # The supervisor is deliberately a fixture, not a change to Air.
    sudo("-u",USER,"tee",dep_root+"/air",input="#!/bin/sh\nexec /usr/bin/sleep infinity\n")
    sudo("chmod","0755",dep_root+"/air")
    service_settings("go-air")
    post(f"/apps/{dep_id}/start")
    assert status_is("Starting") and "systemd: active" in state_page()
    # Observe expiry of the actual monotonic startup grace. This is a test
    # wait, not an artificial delay in the application or unit.
    deadline=time.monotonic()+105
    while time.monotonic()<deadline:
        if status_is("Unavailable"):break
        time.sleep(3)
    else:raise AssertionError("active supervisor without port was not marked unavailable")
    assert sudo("systemctl","show","--property=ActiveState","--value",app_unit)=="active"
    print("Systemd dependencies, required/optional failure semantics, no restart on save, binary and supervisor port health passed")
finally:
    subprocess.run(["sudo","systemctl","stop",app_unit,ready,failure,cycle],check=False,capture_output=True)
    subprocess.run(["sudo","rm","-f",custom]+["/etc/systemd/system/"+name for name in (ready,failure,cycle)],check=False,capture_output=True)
    subprocess.run(["sudo","systemctl","daemon-reload"],check=False,capture_output=True)
