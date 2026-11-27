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
        data = urllib.parse.urlencode(data).encode()
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
        data+=base64.b64decode(result["output_base64"])
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

assert len(rpc(a_conn,"tools/list")["result"]["tools"])==15
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
assert len(rpc(a_conn,"tools/list")["result"]["tools"])==15
out,code=collect(a_conn,live)
assert code==0 and "survived-panel-close" in out
sudo("systemctl","start","open-go-panel.service")
wait_health(PANEL)
request(PANEL, "/login", {"username":"admin","password":password}, expected=303)
page,_=request(PANEL,"/")
csrf=re.search(r'name="ogp_csrf" value="([^"]+)"',page).group(1)
sudo("systemctl","restart","open-go-panel-mcp.service")
wait_health(MCP)
assert len(rpc(a_conn,"tools/list")["result"]["tools"])==15
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
assert len(rpc(b_conn,"tools/list")["result"]["tools"])==15
request(MCP,a_conn[1]+"/token",{"grant_type":"refresh_token","client_id":a_conn[3],"resource":a_conn[2],"refresh_token":a_conn[4]["refresh_token"]},expected=404)
request(MCP,"/admin",expected=404)
print("MCP OAuth, file isolation, host PTY, downloads, sudo/root, policy rotation and revocation passed")
