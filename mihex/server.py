"""HTTP/WebSocket server for MiHex.

Implements a lightweight HTTP server with WebSocket support (RFC 6455),
serving the dashboard UI and providing real-time state updates via
WebSocket broadcast. No external dependencies required.
"""

from json import loads, dumps
from asyncio import Queue, get_running_loop, start_server, IncompleteReadError
from os.path import join, isfile, splitext, normpath, dirname, abspath, commonpath
from os import stat
from email.utils import formatdate
from http import HTTPStatus
from hashlib import sha1
from base64 import b64encode, b64decode
from .entity import Entity, _OBJS
from .runner import exec
from .tracker import fast, slow, track
from logging import getLogger, DEBUG
_LOG = getLogger(__name__)

# ── Constants ──

_WS_GUID = '258EAFA5-E914-47DA-95CA-C5AB0DC85B11'

_JSON_TYPE = 'application/json; charset=utf-8'
_PRIMITIVE = (int, float, str, bool, list, tuple, dict)

# Connection and payload limits for resource-constrained routers
_MAX_WS_CONNS = 8       # Maximum concurrent WebSocket connections
_MAX_WS_PAYLOAD = 65536 # Maximum single-frame payload size (64KB)

_MIME = {
    '.htm': 'text/html; charset=utf-8',
    '.html': 'text/html; charset=utf-8',
    '.css': 'text/css; charset=utf-8',
    '.js': 'application/javascript; charset=utf-8',
    '.json': 'application/json; charset=utf-8',
    '.woff2': 'font/woff2',
    '.png': 'image/png',
}

# ── Module state ──

_token = None
_queue = None
_conns = []


# ── WebSocket (minimal RFC 6455 implementation, protocol layer only) ──

class WebSocket:
    """Asyncio-based WebSocket connection wrapping RFC 6455 protocol.

    Business logic is delegated to the upper layer (WebServer) via
    the on_message callback.
    """

    def __init__(self, reader, writer, on_message, max_payload=_MAX_WS_PAYLOAD):
        self._r = reader
        self._w = writer
        self._on_message = on_message
        self._max_payload = max_payload

    async def recv(self):
        """Receive one text message; returns None if the connection is closed."""
        try:
            fragments = []
            while True:
                b1, b2 = await self._r.readexactly(2)
                fin = b1 & 0x80
                opcode = b1 & 0x0F
                length = b2 & 0x7F

                if length == 126:
                    length = int.from_bytes(await self._r.readexactly(2), 'big')
                elif length == 127:
                    length = int.from_bytes(await self._r.readexactly(8), 'big')

                if length > self._max_payload:
                    _LOG.warning("WebSocket payload too large (%d bytes), closing connection", length)
                    return None

                mask = await self._r.readexactly(4) if b2 & 0x80 else b''
                payload = await self._r.readexactly(length) if length else b''

                if mask and payload:
                    payload = bytes(b ^ mask[i & 3] for i, b in enumerate(payload))

                if opcode == 0x8:  # close
                    self._frame(0x8, b'')
                    await self._w.drain()
                    return None
                if opcode == 0x9:  # ping → pong
                    self._frame(0xA, payload)
                    await self._w.drain()
                    continue
                fragments.append(payload)
                if fin:
                    return b''.join(fragments).decode('utf-8')
        except (IncompleteReadError, ConnectionError):
            return None

    async def send(self, msg):
        """Send a text message to the client."""
        if isinstance(msg, str):
            msg = msg.encode('utf-8')
        self._frame(0x1, msg)
        await self._w.drain()

    async def run(self, key):
        """Complete the WebSocket handshake and run the receive loop."""
        accept = b64encode(sha1((key + _WS_GUID).encode()).digest()).decode()
        self._w.write((
            'HTTP/1.1 101 Switching Protocols\r\n'
            'Upgrade: websocket\r\n'
            'Connection: Upgrade\r\n'
            f'Sec-WebSocket-Accept: {accept}\r\n\r\n'
        ).encode())
        await self._w.drain()

        try:
            while True:
                msg = await self.recv()
                if msg is None:
                    break  # recv() already echoed close frame for opcode 0x8
                if msg == 'close':
                    self._frame(0x8, b'')
                    await self._w.drain()
                    break
                await self._on_message(self, msg)
        except Exception as e:
            _LOG.debug("WebSocket error: %s", e)
        _LOG.debug("WebSocket closed")

    def _frame(self, opcode, payload):
        """Build and write a WebSocket frame."""
        n = len(payload)
        if n < 126:
            hdr = bytes([0x80 | opcode, n])
        elif n < 65536:
            hdr = bytes([0x80 | opcode, 126]) + n.to_bytes(2, 'big')
        else:
            hdr = bytes([0x80 | opcode, 127]) + n.to_bytes(8, 'big')
        self._w.write(hdr + payload)


# ── WebServer (HTTP routing + business dispatch + service registry) ──

_services = {}


def service(func):
    """Register a method as a remotely callable service via HTTP/WS."""
    _services[func.__name__] = func
    return func


def state(ent):
    """Extract uppercase attributes from an entity as a state dict."""
    return {k: v for k in dir(ent) if k.isupper() and (v := getattr(ent, k)) is not None}


@service
def states(_=None):
    """Return state dicts for all Entity instances."""
    return [state(o) for o in _OBJS if isinstance(o, Entity)]


@service
def objects(_=None):
    """Return all uppercase primitive attributes for all registered objects."""
    return [{k: v for k in dir(o) if k[0].isupper() and isinstance(v := getattr(o, k), _PRIMITIVE)} for o in _OBJS]


class WebServer:
    """HTTP/WebSocket server: route dispatch, WS callbacks, service management.

    Functions decorated with @service are automatically registered and can be
    invoked via both HTTP and WebSocket. Add a new service by simply defining
    a @service function in this module.
    """

    # ── HTTP request handler ──

    async def _serve(self, reader, writer):
        def respond(status, body, content_type='text/plain; charset=utf-8'):
            if isinstance(body, str):
                body = body.encode('utf-8')
            da = 'WWW-Authenticate: Basic realm="MiHex"\r\n' if status == 401 else ''
            writer.write((
                f'HTTP/1.1 {status} {HTTPStatus(status).phrase}\r\n'
                f'Content-Type: {content_type}\r\n'
                f'{da}'
                f'Content-Length: {len(body)}\r\n'
                f'Connection: close\r\n\r\n'
            ).encode('iso-8859-1') + body)

        try:
            raw = await reader.readuntil(b'\r\n\r\n')
            request_line, *header_lines = raw.decode('iso-8859-1').splitlines()
            headers = {}
            for line in header_lines:
                if ': ' in line:
                    k, v = line.split(': ', 1)
                    headers[k.lower()] = v
            url_path, _, query = request_line.split(' ')[1].partition('?')
            authed = _token is None or query == _token
            if not authed and _token is not None:
                auth = headers.get('authorization', '')
                if auth.startswith('Basic '):
                    try:
                        authed = b64decode(auth[6:]).decode('utf-8').rpartition(':')[2] == _token
                    except Exception:
                        pass

            if headers.get('upgrade', '').lower() == 'websocket':
                if not authed:
                    respond(401, 'Unauthorized')
                    return
                if len(_conns) >= _MAX_WS_CONNS:
                    respond(503, 'Service Unavailable')
                    return
                ws = WebSocket(reader, writer, self._on_message)
                _conns.append(ws)
                fast()
                try:
                    await ws.run(headers.get('sec-websocket-key', ''))
                finally:
                    if ws in _conns:
                        _conns.remove(ws)
                    if not _conns:
                        slow()
            elif (name := url_path.lstrip('/')) in _services:
                if not authed:
                    respond(401, 'Unauthorized')
                else:
                    get_running_loop().create_task(track())
                    respond(200, dumps(_services[name](), ensure_ascii=False, indent=1), _JSON_TYPE)
            else:
                if url_path == '/':
                    url_path = '/dash.htm'
                www = join(dirname(abspath(__file__)), 'www')
                fp = normpath(join(www, url_path.lstrip('/')))
                if commonpath([fp, www]) == www and isfile(fp):
                    with open(fp, 'rb') as f:
                        body = f.read()
                    # mtime 复验：内容/时间没变 -> 304 不重下 body，变了 -> 200 立刻生效
                    ext = splitext(fp)[1].lower()
                    etag = '"' + sha1(body).hexdigest() + '"'
                    lm = formatdate(stat(fp).st_mtime, usegmt=True)
                    hit = headers.get('if-none-match') == etag or headers.get('if-modified-since') == lm
                    head = (f'HTTP/1.1 {"304" if hit else "200"} {"Not Modified" if hit else "OK"}\r\n'
                            + ('' if hit else f'Content-Type: {_MIME.get(ext, "application/octet-stream")}\r\nContent-Length: {len(body)}\r\n')
                            + f'ETag: {etag}\r\nLast-Modified: {lm}\r\n'
                            + 'Cache-Control: no-cache\r\nConnection: close\r\n\r\n')
                    writer.write(head.encode('iso-8859-1') + (b'' if hit else body))
                else:
                    respond(404, 'Not Found')
        except Exception as e:
            _LOG.debug("Connection error: %s", e)
        finally:
            writer.close()
            try:
                await writer.wait_closed()
            except Exception:
                pass

    # ── WebSocket message handler ──

    async def _on_message(self, ws, msg):
        try:
            data = loads(msg)
            if isinstance(data, list):
                get_running_loop().create_task(self._exec(data))
            else:
                call = data.get('call')
                func = _services.get(call)
                if not callable(func):
                    raise Exception(f"Unknown service call: {call}")
                await _send(func(data))
        except Exception as e:
            _LOG.error("WebSocket error: %s %s", e, msg, exc_info=_LOG.isEnabledFor(DEBUG))
            await _send("WebSocket error: %s %s" % (e, msg))

    async def _exec(self, data):
        try:
            result = await exec(data)
            await _send({data[0]: result})
        except Exception as e:
            _LOG.error("Execution error: %s %s", e, data, exc_info=_LOG.isEnabledFor(DEBUG))
            await _send("Execution error: %s %s" % (e, data))


# ── Server startup ──

async def setup(conf):
    """Initialize the HTTP/WebSocket server from configuration."""
    global _token, _queue
    _token = conf.get('token')
    _queue = Queue()

    context = None
    if cert := conf.get('cert'):
        from ssl import create_default_context, Purpose
        context = create_default_context(Purpose.CLIENT_AUTH)
        context.load_cert_chain(certfile=cert, keyfile=conf.get('key'))

    bind = conf.get('bind')
    port = conf.get('port', 88)
    scheme = 'https' if context else 'http'
    if not (host := conf.get('host')):
        if bind and bind not in ('0.0.0.0', '::'):
            host = bind
        else:
            from socket import socket, AF_INET, SOCK_DGRAM
            try:
                with socket(AF_INET, SOCK_DGRAM) as s:
                    s.connect(('10.255.255.1', 80))
                    host = s.getsockname()[0]
            except Exception:
                host = 'localhost'
    token_suffix = f'?{_token}' if _token else ''
    server = WebServer()
    await start_server(server._serve, bind, port, ssl=context, backlog=16)
    get_running_loop().create_task(_worker())
    _LOG.warning("%s://%s:%s/%s", scheme, host, port, token_suffix)
    return [Entity(c, k) for k, v in conf.items() if type(v) is list for c in v]


# ── Message broadcast ──

async def _worker():
    """Background task: dequeue and broadcast messages to all WebSocket connections."""
    while True:
        msg = await _queue.get()
        for ws in list(_conns):
            try:
                await ws.send(msg)
            except Exception:
                if ws in _conns:
                    _conns.remove(ws)
                if not _conns:
                    slow()
        _queue.task_done()


async def _send(message):
    """Enqueue a message for broadcast to all WebSocket clients."""
    text = message if isinstance(message, str) else dumps(message, ensure_ascii=False)
    await _queue.put(text)


async def send_state(ent):
    """Broadcast entity state to all connected WebSocket clients."""
    if not _conns:
        return
    if isinstance(ent, list):
        obj = state(ent[0]) if len(ent) == 1 else [state(e) for e in ent]
    else:
        obj = state(ent)
    await _send(dumps(obj, ensure_ascii=False))