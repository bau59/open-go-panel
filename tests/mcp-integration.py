#!/usr/bin/env python3
"""Disposable Ubuntu runner only. Never run on a production VPS."""
import base64
import hashlib
import http.cookiejar
import json
import os
import re
import secrets
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

def authorize(app_id):
    post(f"/apps/{app_id}/chatgpt/enable")
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

assert len(rpc(a_conn,"tools/list")["result"]["tools"])==8
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
    command="cat "+target
    result=call(a_conn,"run_command",{"command":command},failed=True)
    assert "secret sibling" not in result["output"]
    call(a_conn,"run_command",{"command":"printf bad > "+target},failed=True)
assert sudo("-u",USER,"cat",root_b+"/secret.txt")=="secret sibling"
assert call(b_conn,"read_file",{"path":"secret.txt"})["content"]=="secret sibling\n"
result=call(a_conn,"run_command",{"command":"id -u; pwd; printf ok > command.txt"})
assert result["output"].splitlines()==[sudo("id","-u",USER),"/workspace"]
assert sudo("stat","-c","%U",root_a+"/command.txt")==USER
result=call(a_conn,"run_command",{"command":"yes a | head -c 500000"})
assert result["truncated"] and len(result["output"])<=256*1024
marker="mcp-timeout-"+secrets.token_hex(8)
result=call(a_conn,"run_command",{"command":f"sh -c 'sleep 60; echo {marker}' & wait","timeout_seconds":1},failed=True)
assert result["timed_out"]
time.sleep(1)
assert subprocess.run(["sudo","pgrep","-u",USER,"-f",marker],capture_output=True).returncode==1, "orphan process"
sudo("systemctl","stop","open-go-panel.service")
assert len(rpc(a_conn,"tools/list")["result"]["tools"])==8
sudo("systemctl","start","open-go-panel.service")
wait_health(PANEL)
request(PANEL, "/login", {"username":"admin","password":password}, expected=303)
page,_=request(PANEL,"/")
csrf=re.search(r'name="ogp_csrf" value="([^"]+)"',page).group(1)
sudo("systemctl","restart","open-go-panel-mcp.service")
wait_health(MCP)
assert len(rpc(a_conn,"tools/list")["result"]["tools"])==8
sudo("systemctl","is-enabled","open-go-panel-mcp.service")
post(f"/apps/{a}/chatgpt/revoke")
rpc(a_conn,"tools/list",expected=401)
assert len(rpc(b_conn,"tools/list")["result"]["tools"])==8
request(MCP,a_conn[1]+"/token",{"grant_type":"refresh_token","client_id":a_conn[3],"resource":a_conn[2],"refresh_token":a_conn[4]["refresh_token"]},expected=404)
request(MCP,"/admin",expected=404)
print("MCP authorization, file ownership, project isolation, timeout, restart and revocation passed")
