"""Command dispatch and template expansion for MiHex.

Handles command routing to registered entities/modules and provides
template expansion with {{}} placeholder syntax. Also includes an
async HTTP fetch utility using urllib (non-blocking via thread pool).
"""

from json import loads, dumps
from asyncio import iscoroutine
from urllib.request import Request, urlopen
from .entity import _OBJS, runin

_FETCH_TIMEOUT = 10

_conf = {}


async def setup(conf):
    """Store runner configuration (template definitions)."""
    global _conf
    _conf = conf


async def dispatch(args):
    """Dispatch a command to the matching entity/module.

    Supports two calling conventions:
      - Entity method: <EntityName> <Method> [args...]
      - Entity service: <EntityName> [args...]  (calls the 'service' method)
    """
    if not args:
        return None
    arg0 = args[0]
    argc = len(args)
    if m := next((x for x in _OBJS if x.NAME == arg0), None):
        if argc > 1 and hasattr(m, args[1]):
            service = args[1]
            if argc == 2:
                args = None
            elif argc == 3:
                args = args[2]
            else:
                args = args[2:]
        # elif not hasattr(m, 'service'):
        # 	return None
        else:
            service = 'service'
            args = args[1:]
        r = getattr(m, service)(args)
        if iscoroutine(r):
            r = await r
        # TODO: send_state(m)
        return r


async def expand(text):
    """Expand {{placeholder}} templates in text by dispatching embedded commands."""
    ret = ''
    pos0 = 0
    while True:
        pos1 = text.find('{{', pos0)
        if pos1 == -1:
            ret += text[pos0:]
            break
        ret += text[pos0:pos1]
        pos2 = text.find('}}', pos1 + 2)
        if pos2 == -1:
            ret += text[pos1:]
            break
        args = text[pos1 + 2:pos2]
        pos0 = pos2 + 2
        r = await dispatch(args.split(' '))
        if r is not None:
            ret += str(r)
    return ret


async def exec(args):
    """Expand templates in args and dispatch the resulting command."""
    return await dispatch([await expand(_conf[x]) if x in _conf else x for x in args])


async def fetch(url, data=None, headers=None, ret_json=True):
    """Async HTTP request using urllib in a thread pool (non-blocking).

    Automatically serializes data: str -> encode, non-bytes -> JSON.
    """
    return await runin(_fetch, url, data, headers, ret_json)


def _fetch(url, data, headers, ret_json=True):
    """Synchronous HTTP fetch (runs in thread pool)."""
    if isinstance(data, str):
        data = data.encode()
    elif not isinstance(data, (bytes, type(None))):
        data = dumps(data).encode()
        headers = {**(headers or {}), 'Content-Type': 'application/json'}
    req = Request(url, data=data, headers=headers or {})
    with urlopen(req, timeout=_FETCH_TIMEOUT) as r:
        raw = r.read()
        return loads(raw) if ret_json else raw