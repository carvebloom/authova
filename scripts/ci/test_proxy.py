"""New-CI-only normal TLS proxy + single-recipient STARTTLS SMTP sink.

No arbitrary upstream URL, no mail API, no body logs or mail persistence. The sink
can hold at most one message and serves only its fixed new recipient/control key.
"""
import email.parser
import email.policy
import hmac
import http.client
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
import json
from pathlib import Path
import re
import signal
import socketserver
import ssl
import threading
import time


class Inbox:
    def __init__(self, recipient, control):
        self.recipient=recipient;self.control=control;self.messages=[];self.lock=threading.Lock()
    def store(self, recipient, body):
        if recipient != self.recipient or len(body)>65536:raise ValueError('SMTP_SCOPE')
        message=email.parser.BytesParser(policy=email.policy.default).parsebytes(body)
        text='\n'.join(p.get_content() for p in message.walk() if p.get_content_type()=='text/plain')
        codes=re.findall(r'(?<!\d)(\d{6})(?!\d)',text)
        if len(set(codes)) != 1:raise ValueError('SMTP_CODE')
        with self.lock:
            if self.messages:raise ValueError('SMTP_AMBIGUOUS')
            self.messages.append({'observed':time.time(),'code':codes[0]})
    def consume(self, token, since):
        if not hmac.compare_digest(token,self.control):raise ValueError('SMTP_CONTROL')
        with self.lock:
            if not self.messages:return None
            item=self.messages[0]
            if item['observed']<since:raise ValueError('SMTP_TIME')
            return item  # Same one flow; caller never resends or searches old mail.


def smtp_handler(inbox, context):
    class SMTP(socketserver.StreamRequestHandler):
        def handle(self):
            self.request.settimeout(10);secure=False;recipient=None
            def send(line):self.wfile.write(line+b'\r\n');self.wfile.flush()
            send(b'220 isolated-ci')
            for _ in range(20):
                line=self.rfile.readline(4097)
                if not line or len(line)>4096:return
                command=line.strip()
                if command.upper().startswith(b'EHLO'):
                    send(b'250-isolated-ci');send(b'250 STARTTLS' if not secure else b'250 OK')
                elif command.upper()==b'STARTTLS' and not secure:
                    send(b'220 TLS ready')
                    self.rfile.close();self.wfile.close()
                    self.connection=context.wrap_socket(self.connection,server_side=True)
                    self.rfile=self.connection.makefile('rb');self.wfile=self.connection.makefile('wb');secure=True
                elif not secure:send(b'530 TLS required')
                elif command.upper().startswith(b'MAIL FROM:'):send(b'250 OK')
                elif command.upper().startswith(b'RCPT TO:'):
                    target=command[8:].strip().strip(b'<>').decode('ascii')
                    if target!=inbox.recipient:send(b'550 outside scope');return
                    recipient=target;send(b'250 OK')
                elif command.upper()==b'DATA' and recipient:
                    send(b'354 input');body=bytearray()
                    while True:
                        item=self.rfile.readline(4097)
                        if item==b'.\r\n':break
                        if not item or len(item)>4096 or len(body)+len(item)>65536:return
                        body.extend(item[1:] if item.startswith(b'..') else item)
                    try:inbox.store(recipient,bytes(body))
                    except ValueError:send(b'554 rejected');return
                    send(b'250 accepted')
                elif command.upper()==b'QUIT':send(b'221 bye');return
                elif command.upper()==b'RSET':recipient=None;send(b'250 OK')
                else:send(b'502 unsupported')
    return SMTP


def handler(role, inbox):
    class Proxy(BaseHTTPRequestHandler):
        protocol_version='HTTP/1.1'
        def log_message(self,*args):pass
        def handle_one_request(self):
            try:super().handle_one_request()
            except BaseException:self.close_connection=True
        def serve(self):
            if self.path=='/_ci/mail' and role=='core':
                try:
                    since=float(self.headers.get('X-CI-Since','nan'))
                    if not (0<since<=time.time()):raise ValueError('TIME')
                    value=inbox.consume(self.headers.get('X-CI-Control',''),since)
                    data=json.dumps(value).encode()
                    self.send_response(200 if value else 202)
                    self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(data)))
                    self.send_header('Cache-Control','no-store');self.end_headers();self.wfile.write(data);return
                except BaseException:self.send_error(403);return
            if self.path.startswith('/_ci/') or not self.path.startswith('/') or self.path.startswith('//'):
                self.send_error(404);return
            lengths=self.headers.get_all('Content-Length',[])
            if self.headers.get_all('Transfer-Encoding',[]) or len(lengths)>1:
                self.send_error(400);return
            try:size=int(lengths[0]) if lengths else 0
            except ValueError:self.send_error(400);return
            if size<0 or size>1048576:self.send_error(413);return
            body=self.rfile.read(size)
            if len(body)!=size:self.send_error(400);return
            headers={k:v for k,v in self.headers.items() if k.lower() not in ('connection','proxy-connection','transfer-encoding','upgrade')}
            upstream=http.client.HTTPConnection(role,8080 if role=='core' else 8090,timeout=15)
            try:
                upstream.request(self.command,self.path,body if size else None,headers)
                response=upstream.getresponse();data=response.read(4*1024*1024+1)
                if len(data)>4*1024*1024:raise ValueError('RESPONSE_SIZE')
                self.send_response(response.status)
                for k,v in response.getheaders():
                    if k.lower() not in ('connection','transfer-encoding','content-length','server','date'):self.send_header(k,v)
                self.send_header('Content-Length',str(len(data)));self.end_headers();self.wfile.write(data)
            finally:upstream.close()
        do_GET=do_POST=do_PATCH=do_DELETE=do_PUT=do_OPTIONS=serve
    return Proxy


def main():
    root=Path('/run/ci');config=json.loads((root/'control.json').read_bytes())
    inbox=Inbox(config['recipient'],config['control'])
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.minimum_version=ssl.TLSVersion.TLSv1_2
    context.load_cert_chain(root/'tls.crt',root/'tls.key')
    servers=[]
    for role,port in (('core',8443),('console',8444)):
        server=ThreadingHTTPServer(('0.0.0.0',port),handler(role,inbox));server.daemon_threads=True
        server.socket=context.wrap_socket(server.socket,server_side=True);servers.append(server)
    class TCP(socketserver.ThreadingTCPServer):
        allow_reuse_address=False;daemon_threads=True
    servers.append(TCP(('0.0.0.0',1025),smtp_handler(inbox,context)))
    threads=[threading.Thread(target=s.serve_forever) for s in servers]
    stop=threading.Event()
    signal.signal(signal.SIGTERM,lambda *_:stop.set())
    for t in threads:t.start()
    stop.wait()
    for s in servers:s.shutdown();s.server_close()
    for t in threads:t.join(5)
    if any(t.is_alive() for t in threads):raise SystemExit(1)


if __name__=='__main__':
    try:main()
    except BaseException:raise SystemExit('CI_PROXY_FAILED') from None
