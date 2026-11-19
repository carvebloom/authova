"""Bounded normal-TLS owner HTTP transport. No proxy inheritance or redirects."""
import http.client
import http.cookiejar
from http.cookies import SimpleCookie
from html.parser import HTMLParser
import json
import re
import socket
import ssl
import time
import urllib.error
import urllib.parse
import urllib.request

from host_admission import strict_json
from private_native import Unknown
from release_contract import Refused, need


HTTP_CHECKPOINTS=('NONE','APP_CREATE','CLIENT_CREATE','AUTHORIZE','CHALLENGE','MAIL','VERIFY','REGISTER','PASSWORD','USER_READBACK','CONSENT','REPLAY','LOGOUT')
HTTP_STAGES=('normal-tls-health-and-static-assets','guest-and-authorization-boundaries','management-password-mfa-logout',
             'oidc-register','oidc-password','oidc-consent','oidc-replay-logout','operator-password-and-session-revocation','owned-test-io-and-session-closure')


def valid_failure_frame(value,rc):
    return (type(rc) is int and type(value) is dict and set(value)=={'result','code','checkpoint','status','ordinal'}
            and ((rc==1 and value['result']=='FAIL' and value['code']=='CI_HTTP_ASSERTION')
                 or (rc==2 and value['result']=='UNKNOWN' and value['code']=='CI_HTTP_OUTCOME_UNKNOWN'))
            and type(value['checkpoint']) is str and value['checkpoint'] in HTTP_CHECKPOINTS
            and (value['status'] is None or type(value['status']) is int and 100<=value['status']<=599)
            and type(value['ordinal']) is int and 0<=value['ordinal']<=256)



class KnownUnhealthy(Refused):pass


def static_assets(page):
    """Resolve only root asset files; never URL-join arbitrary document input."""
    need(type(page) is bytes and len(page)<=4*1024*1024,'STATIC_ASSET_BODY')
    assets={}
    class Entries(HTMLParser):
        def handle_starttag(self,tag,attrs):
            if tag=='base':need(not any(k=='href' for k,_ in attrs),'STATIC_ASSET_BASE')
            if tag in ('script','link'):
                need(all(sum(k==name for k,_ in attrs)<=1 for name in ('src','href','rel')),'STATIC_ASSET_PATH')
            values=dict(attrs)
            entry='src' if tag=='script' and 'src' in values else (
                'href' if tag=='link' and 'stylesheet' in (values.get('rel') or '').lower().split() else None)
            for key,value in attrs:
                selected=key==entry or (key in ('src','href') and type(value) is str
                    and value.startswith(('/assets/','./assets/')))
                if not selected:continue
                need(sum(k==key for k,_ in attrs)==1 and type(value) is str,'STATIC_ASSET_PATH')
                path=value[1:] if value.startswith('./assets/') else value
                need(re.fullmatch('/assets/[A-Za-z0-9_-][A-Za-z0-9_.-]*',path) is not None
                     and '..' not in path,'STATIC_ASSET_PATH')
                assets[path]=None
            if entry is not None:need(entry in values,'STATIC_ASSET_PATH')
    try:text=page.decode('utf-8')
    except UnicodeDecodeError:raise KnownUnhealthy('STATIC_ASSET_HTML') from None
    parser=Entries();parser.feed(text);parser.close()
    return list(assets)


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self,*args):return None


class LoopbackTLS(http.client.HTTPSConnection):
    # New isolated fixture's public origin is host.docker.internal so the same
    # issuer works inside its verifier container. Host runner uses loopback TCP,
    # but validates the exact URL hostname/SNI against its explicit new CA.
    def connect(self):
        self.sock=self._context.wrap_socket(socket.create_connection(('127.0.0.1',getattr(self,'ci_port',self.port)),self.timeout),server_hostname=self.host)


class HTTPS(urllib.request.HTTPSHandler):
    def __init__(self,context,loopback,ports=None):super().__init__(context=context);self.loopback=loopback;self.ports=ports or {}
    def https_open(self,request):
        def factory(host,**kwargs):
            result=LoopbackTLS(host,**kwargs);result.ci_port=self.ports.get(result.port,result.port);return result
        return self.do_open(factory if self.loopback else http.client.HTTPSConnection,request,context=self._context)


class Browser:
    def __init__(self,origins,context,guard,*,loopback=False,cookies=(),ports=None):
        self.origins=set(origins);self.guard=guard;self.jar=http.cookiejar.CookieJar()
        for row in cookies:self.jar.set_cookie(http.cookiejar.Cookie(**row))
        self.opener=urllib.request.build_opener(urllib.request.ProxyHandler({}),HTTPS(context,loopback,ports),NoRedirect(),urllib.request.HTTPCookieProcessor(self.jar))
        self.calls=[]
    def cookie_state(self):
        fields=('version','name','value','port','port_specified','domain','domain_specified','domain_initial_dot','path','path_specified','secure','expires','discard','comment','comment_url')
        return [dict({f:getattr(c,f) for f in fields},rest=c._rest,rfc2109=c.rfc2109) for c in self.jar]
    def request(self,url,*,method='GET',body=None,form=None,headers=None,expected=(200,),limit=4*1024*1024):
        u=urllib.parse.urlsplit(url)
        need(u.scheme=='https' and u.scheme+'://'+u.netloc in self.origins and not u.username and not u.password and not u.fragment,'HTTP_SCOPE')
        hdr={'Accept-Encoding':'identity'};hdr.update(headers or {})
        if body is not None:body=json.dumps(body).encode();hdr['Content-Type']='application/json'
        elif form is not None:body=urllib.parse.urlencode(form).encode();hdr['Content-Type']='application/x-www-form-urlencoded'
        self.guard.full(20,20);self.guard();i=time.time();m=time.monotonic();response=None;first=None
        try:
            request=urllib.request.Request(url,data=body,headers=hdr,method=method)
            try:response=self.opener.open(request,timeout=15)
            except urllib.error.HTTPError as e:response=e
            except (OSError,urllib.error.URLError):raise Unknown('HTTP_RESPONSE_UNKNOWN') from None
            self.guard()
            lengths=response.headers.get_all('Content-Length',[])
            types=response.headers.get_all('Content-Type',[])
            need(len(lengths)<=1 and len(types)<=1 and not response.headers.get_all('Content-Encoding',[]),'HTTP_HEADERS')
            data=bytearray()
            while True:
                self.guard();part=response.read(min(65536,limit+1-len(data)));self.guard()
                if not part:break
                data.extend(part);need(len(data)<=limit,'HTTP_BODY_LIMIT')
            need(not lengths or (lengths[0].isdecimal() and int(lengths[0])==len(data)),'HTTP_LENGTH')
            status=response.status;h=response.headers
            if status not in expected:raise KnownUnhealthy('HTTP_KNOWN_STATUS')
            return bytes(data),h,status
        except BaseException as exc:first=exc;raise
        finally:
            if response is not None:
                try:response.close()
                except BaseException:
                    if first is None:raise Unknown('HTTP_CLOSE_UNKNOWN') from None
                    first.close_secondary=getattr(first,'close_secondary',())+('HTTP_CLOSE',)
            if first is None:
                self.guard()
                need(time.time()<i+20 and time.monotonic()<m+20,'HTTP_LATE_UNKNOWN')
            self.calls.append({'status':getattr(response,'status',None),'known_return':response is not None,
                               'started_at':i,'finished_at':time.time(),'timely':first is None})
    def json(self,url,**kw):return strict_json(self.request(url,**kw)[0])


def context(ca_pem=None):
    if ca_pem is None:return ssl.create_default_context()
    return ssl.create_default_context(cadata=ca_pem)


def readonly_acceptance(profile,guard):
    ca=profile.get('ca_pem')
    browser=Browser((profile['core_origin'],profile['console_origin']),context(ca),guard)
    browser.request(profile['core_origin']+'/readyz')
    browser.request(profile['console_origin']+'/healthz')
    page,_,_=browser.request(profile['console_origin']+'/')
    assets=static_assets(page)
    if not assets:raise KnownUnhealthy('STATIC_ASSET_ENTRY')
    for path in assets:browser.request(profile['console_origin']+path)
    browser.request(profile['console_origin']+'/bff/me',expected=(401,))
    return browser.calls
