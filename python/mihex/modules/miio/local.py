"""MiIO local (LAN) protocol client for MiHex.

Talks directly to a device over UDP port 54321 using its device token,
bypassing the Xiaomi cloud entirely. This is the same wire protocol used by
python-miio/Home Assistant's local integration; documented here because
mihex implements it from scratch (see aes.py) rather than depending on a
third-party miio client.

Packet layout (all big-endian), 32-byte header + optional encrypted body:

    magic(2)=0x2131  length(2)  unknown(4)  device_id(4)  stamp(4)  checksum(16)  [encrypted(N)]

Handshake ("Hello"): send a 32-byte packet of 0x21 0x31 0x00 0x20 followed by
28 bytes of 0xFF. The device replies with its own header containing
device_id and stamp (its uptime counter in seconds), which are then echoed
back (stamp advanced by elapsed wall-clock time) on every subsequent
request so the device can reject stale/replayed packets.

Request/response bodies are JSON (`{"id", "method", "params"}` /
`{"id", "result"}`), encrypted AES-128-CBC with key=MD5(token),
iv=MD5(key+token), and the checksum is MD5(header[:16] + token + encrypted).

CAUTION: this implementation is verified against the AES-128 FIPS-197 test
vectors and round-trips its own encrypt/decrypt, but has not been exercised
against real Xiaomi hardware — treat it as best-effort. Every call site in
this package falls back to the cloud API on any local failure, so a bug
here degrades to the (working) cloud path rather than breaking control.
"""

from asyncio import DatagramProtocol, Lock, Queue, get_running_loop, wait_for, TimeoutError as AsyncTimeoutError
from hashlib import md5
from json import dumps, loads
from struct import pack
from time import monotonic
from logging import getLogger
from .aes import cbc_encrypt, cbc_decrypt
_LOG = getLogger(__name__)

_PORT = 54321
_TIMEOUT = 2
_HELLO = b'\x21\x31\x00\x20' + b'\xff' * 28


class _Protocol(DatagramProtocol):
    """Asyncio UDP protocol: hands each received datagram to a queue."""

    def __init__(self):
        self.queue = Queue()

    def datagram_received(self, data, addr):
        self.queue.put_nowait(data)

    def error_received(self, exc):
        self.queue.put_nowait(exc)


class LocalDevice:
    """LAN-local MiIO client for a single device (host + token)."""

    def __init__(self, host, token, port=_PORT, timeout=_TIMEOUT):
        self._host = host
        self._port = port
        self._timeout = timeout
        self._token = bytes.fromhex(token)
        self._key = md5(self._token).digest()
        self._iv = md5(self._key + self._token).digest()
        self._transport = None
        self._protocol = None
        self._device_id = None
        self._stamp = 0
        self._stamp_time = 0
        self._msg_id = 0
        self._lock = Lock()

    async def _get_transport(self):
        if self._transport is None:
            loop = get_running_loop()
            self._transport, self._protocol = await loop.create_datagram_endpoint(
                _Protocol, remote_addr=(self._host, self._port))
        return self._transport, self._protocol

    async def _recv(self, protocol):
        while not protocol.queue.empty():
            protocol.queue.get_nowait()  # drop anything stale before sending
        try:
            data = await wait_for(protocol.queue.get(), timeout=self._timeout)
        except AsyncTimeoutError:
            raise TimeoutError(f"{self._host}: no response")
        if isinstance(data, Exception):
            raise data
        return data

    async def _handshake(self):
        transport, protocol = await self._get_transport()
        transport.sendto(_HELLO)
        data = await self._recv(protocol)
        if len(data) < 32:
            raise ValueError(f"{self._host}: handshake response too short")
        self._device_id = data[8:12]
        self._stamp = int.from_bytes(data[12:16], 'big')
        self._stamp_time = monotonic()

    async def _do_request(self, method, params):
        transport, protocol = await self._get_transport()
        self._msg_id += 1
        payload = dumps({'id': self._msg_id, 'method': method, 'params': params}).encode()
        encrypted = cbc_encrypt(payload, self._key, self._iv)
        elapsed = self._stamp + int(monotonic() - self._stamp_time)
        header = pack('>HHI', 0x2131, 32 + len(encrypted), 0) + self._device_id + pack('>I', elapsed)
        checksum = md5(header + self._token + encrypted).digest()
        transport.sendto(header + checksum + encrypted)

        data = await self._recv(protocol)
        if len(data) <= 32:
            raise ValueError(f"{self._host}: empty response body")
        resp = loads(cbc_decrypt(data[32:], self._key, self._iv).decode())
        if 'error' in resp:
            raise Exception(f"{self._host}: {resp['error']}")
        return resp.get('result')

    async def request(self, method, params):
        """Send one MiIO local request, retrying once through a fresh handshake."""
        async with self._lock:
            if self._device_id is None:
                await self._handshake()
            try:
                return await self._do_request(method, params)
            except (TimeoutError, ValueError) as e:
                _LOG.debug("%s: retrying after handshake (%s)", self._host, e)
                await self._handshake()
                return await self._do_request(method, params)

    async def get_props(self, iids):
        """Batch property read. iids: list of legacy names, or (siid, piid) tuples."""
        if isinstance(iids[0], str):
            return await self.request('get_prop', iids)
        params = [{'did': f'{s}-{p}', 'siid': s, 'piid': p} for s, p in iids]
        result = await self.request('get_properties', params)
        return [it.get('value') if it.get('code') == 0 else None for it in result]

    async def set_prop(self, iid, value):
        """Single property write. iid: a legacy name, or an (siid, piid) tuple."""
        if isinstance(iid, str):
            result = await self.request('set_' + iid, value if isinstance(value, list) else [value])
            return 0 if result and result[0] == 'ok' else -1
        s, p = iid
        result = await self.request('set_properties', [{'did': f'{s}-{p}', 'siid': s, 'piid': p, 'value': value}])
        return result[0].get('code', -1) if result else -1

    async def action(self, iid, args=None):
        """Invoke a MIoT action. iid: an (siid, aiid) tuple."""
        s, a = iid
        result = await self.request('action', {'did': f'{s}-{a}', 'siid': s, 'aiid': a, 'in': args or []})
        return result.get('code', -1) if isinstance(result, dict) else -1
