"""Bounded RAM-only host CLI transport. Never persists stdout/stderr or argv."""
import os
import selectors
import stat
import subprocess
import time

from release_contract import Refused, need


class Unknown(Refused):
    pass


class Native:
    def __init__(self, tool, docker_host, config_root, *, popen=subprocess.Popen):
        need(type(tool) is dict and set(tool) == {'path', 'identity', 'sha256'}, 'DOCKER_TOOL_PIN')
        need(type(docker_host) is str and docker_host.startswith('unix:///'), 'DOCKER_ENDPOINT')
        self.tool = tool
        self.host = docker_host
        self.config = config_root
        self.popen = popen
        self.records = []
        self.checked = False
        self.observed = None

    def tool_guard(self):
        import hashlib
        from host_adapter import _token
        path = self.tool['path']
        need(type(path) is str and path.startswith('/'), 'DOCKER_TOOL_PATH')
        fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW)
        first = None
        try:
            s = os.fstat(fd)
            need(stat.S_ISREG(s.st_mode) and s.st_nlink == 1 and s.st_size <= 64*1024*1024 and s.st_uid in (0,os.getuid()) and not s.st_mode & 0o6022 and s.st_mode & 0o100, 'DOCKER_TOOL_TYPE')
            actual = list(_token(s))
            if self.tool['identity'] is not None:need(actual == self.tool['identity'], 'DOCKER_TOOL_IDENTITY')
            if not self.checked:
                sha = hashlib.sha256()
                size = 0
                while True:
                    block = os.read(fd, 65536)
                    if not block:break
                    size += len(block)
                    sha.update(block)
                need(size == s.st_size and sha.hexdigest() == self.tool['sha256'], 'DOCKER_TOOL_BYTES')
            need(_token(s) == _token(os.fstat(fd)) == _token(os.stat(path, follow_symlinks=False)), 'DOCKER_TOOL_DRIFT')
            self.checked = True
            if self.tool['identity'] is None:self.tool['identity']=actual
            self.observed={'path':path,'identity':actual,'sha256':self.tool['sha256'],'scope':'FIRST_CURRENT_STABLE_BYTES_NOT_VENDOR_OR_ATOMIC_EXEC_MAPPING'}
        except BaseException as exc:
            first = exc
            raise
        finally:
            from host_adapter import _close
            _close(fd, first, 'TOOL_CLOSE')

    def run(self, tag, argv, guard, *, body=None, whole=30, suffix=20, limit=2*1024*1024):
        need(type(tag) is str and tag.isupper() and tag.replace('_', '').isalnum(), 'COMMAND_TAG')
        need(type(argv) is list and all(type(v) is str and '\x00' not in v for v in argv), 'COMMAND_ARGV')
        need(body is None or type(body) is bytes, 'COMMAND_BODY')
        self.tool_guard()
        guard.full(whole, suffix)
        i, m = time.time(), time.monotonic()
        cap = whole - 5
        need(cap > 0, 'COMMAND_CLOSE_CAP')
        env = {'PATH':'/usr/bin:/bin', 'HOME':self.config, 'DOCKER_CONFIG':self.config,
               'DOCKER_HOST':self.host, 'LANG':'C', 'LC_ALL':'C'}
        p = None
        selector = selectors.DefaultSelector()
        buffers = {'stdout':bytearray(), 'stderr':bytearray()}
        first = None
        close_first = None
        closed = True
        waited = False
        rc = None
        input_view = memoryview(body or b'')
        def body_guard():
            guard()
            need(i <= time.time() < i+cap and m <= time.monotonic() < m+cap, 'CLI_TIMEOUT_UNKNOWN')
        try:
            body_guard()
            p = self.popen([self.tool['path'], *argv], stdin=subprocess.PIPE,
                           stdout=subprocess.PIPE, stderr=subprocess.PIPE, env=env, close_fds=True)
            body_guard()
            for name in ('stdout', 'stderr'):
                stream = getattr(p, name)
                os.set_blocking(stream.fileno(), False)
                selector.register(stream, selectors.EVENT_READ, name)
            if input_view:
                os.set_blocking(p.stdin.fileno(), False)
                selector.register(p.stdin, selectors.EVENT_WRITE, 'stdin')
            else:
                p.stdin.close()
            while selector.get_map():
                body_guard()
                events = selector.select(0.1)
                body_guard()
                for key, _ in events:
                    body_guard()
                    if key.data == 'stdin':
                        try:count = os.write(key.fd, input_view[:65536])
                        except BlockingIOError:continue
                        need(count > 0, 'CLI_STDIN_UNKNOWN')
                        input_view = input_view[count:]
                        if not input_view:
                            selector.unregister(key.fileobj)
                            key.fileobj.close()
                    else:
                        try:chunk = os.read(key.fd, 65536)
                        except BlockingIOError:continue
                        if not chunk:
                            selector.unregister(key.fileobj)
                            key.fileobj.close()
                        else:
                            buffers[key.data].extend(chunk)
                            need(len(buffers[key.data]) <= limit, 'CLI_OUTPUT_UNKNOWN')
                    body_guard()
            # Original command's last five seconds are reserved for owned client wait/close.
            remain = min(i+whole-time.time(), m+whole-time.monotonic())
            need(remain > 0, 'CLI_WAIT_UNKNOWN')
            rc = p.wait(timeout=remain)
            waited = True
            guard()
            need(i <= time.time() < i+whole and m <= time.monotonic() < m+whole, 'CLI_LATE_UNKNOWN')
            self.tool_guard()
        except BaseException as exc:
            first = exc
            raise
        finally:
            # Only this positive local CLI handle, never a daemon/container force action.
            if p is not None:
                if not waited:
                    try:
                        if p.poll() is None:p.kill()
                        p.wait(timeout=2)
                    except BaseException as exc:
                        closed = False
                        if close_first is None:close_first=exc
                        if first is not None:first.close_secondary=getattr(first,'close_secondary',())+('CLI_WAIT_CLOSE',)
                for name in ('stdin', 'stdout', 'stderr'):
                    stream = getattr(p, name, None)
                    if stream is not None:
                        try:stream.close()
                        except BaseException as exc:
                            closed = False
                            if close_first is None:close_first=exc
                            if first is not None:
                                first.close_secondary = getattr(first, 'close_secondary', ()) + ('CLI_STREAM_CLOSE',)
            try:selector.close()
            except BaseException as exc:
                closed = False
                if close_first is None:close_first=exc
                if first is not None:first.close_secondary=getattr(first,'close_secondary',())+('CLI_SELECTOR_CLOSE',)
            finished_i=finished_m=None
            try:
                finished_i,finished_m=time.time(),time.monotonic()
            except BaseException as exc:
                if close_first is None:close_first=exc
                if first is not None:first.close_secondary=getattr(first,'close_secondary',())+('CLI_POST_CLOCK',)
            late = finished_i is None or finished_m is None or not (i <= finished_i < i+whole and m <= finished_m < m+whole)
            self.records.append({'tag':tag, 'exit_code':rc, 'known_return':waited,
                                 'timely':first is None and close_first is None and not late, 'all_selected_IO_closed':closed,
                                 'started_at':i, 'original_monotonic':m, 'elapsed_monotonic':None if finished_m is None else finished_m-m, 'whole_seconds':whole, 'finished_at':finished_i})
            if late and first is not None:first.close_secondary=getattr(first,'close_secondary',())+('CLI_POST_CLOCK',)
            if close_first is not None and first is None:raise close_first
            if late and first is None:
                raise Unknown('CLI_LATE_UNKNOWN')
            if not closed and first is None:
                raise Unknown('CLI_CLOSE_UNKNOWN')
        return rc, bytes(buffers['stdout'])  # stderr remains RAM only, never logged/hashed.

    def require(self, tag, argv, guard, **kwargs):
        rc, body = self.run(tag, argv, guard, **kwargs)
        need(rc == 0, 'CLI_COMMAND_EXIT')
        return body
