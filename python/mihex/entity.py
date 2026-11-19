"""Entity base class and async utilities for MiHex.

Provides the Entity base class used by all device modules, the global
object registry (_OBJS), the shared event loop (_LOOP), and helper
functions for running sync/coro code across threads.
"""

from asyncio import new_event_loop, run_coroutine_threadsafe, get_running_loop
from logging import getLogger, DEBUG
_LOG = getLogger()

_OBJS = []
_LOOP = new_event_loop()


def runin(func, *args):
    """Run a blocking function in the thread pool, returns an awaitable."""
    return get_running_loop().run_in_executor(None, func, *args)


def runco(coro):
    """Submit a coroutine from a non-async thread and block until result.

    Only use from threads (e.g. paho-mqtt callbacks) when you need the result.
    """
    return run_coroutine_threadsafe(coro, _LOOP).result()


def runas(coro):
    """Submit a coroutine from a non-async thread without blocking.

    Returns immediately; errors are logged but not raised.
    """
    def _on_done(fut):
        try:
            fut.result()
        except Exception as e:
            _LOG.error("Runas failed: %s", e, exc_info=_LOG.isEnabledFor(DEBUG))
    run_coroutine_threadsafe(coro, _LOOP).add_done_callback(_on_done)


class Entity:
    """Base class for all MiHex device entities.

    Uppercase attributes (NAME, STAT, ISON, ICON, etc.) are automatically
    exposed to the dashboard and broadcast via WebSocket on change.
    """

    def __init__(self, conf, kind=None):
        for k, v in conf.items():
            setattr(self, k, v)
        if not hasattr(self, 'TYPE'):
            self.TYPE = kind

    async def on_turn(self, arg):
        """Handle on/off toggle from the dashboard."""
        self.ISON = arg
        from .server import send_state
        await send_state(self)

    async def on_mode(self, arg):
        """Handle mode change from the dashboard."""
        pass

    async def on_tune(self, arg):
        """Handle tune (slider/toggle) adjustment from the dashboard."""
        pass