"""Device state polling tracker for MiHex.

Implements a dual-speed (fast/slow) polling mechanism. When at least one
WebSocket client is connected, the tracker switches to fast mode for
real-time updates. When no clients are connected, it falls back to
slow mode to conserve resources on embedded devices.
"""

from .entity import _LOGS, _OBJS, _LOOP
from asyncio import iscoroutine, wait_for, TimeoutError as async_timeout, gather, Lock, Event

_fast = 60
_slow = 3600
_gap = 3600
_event = None
_track_lock = None


def setup(conf):
    """Initialize the tracker with configured or default intervals."""
    global _fast, _slow, _event, _track_lock
    _fast = conf.get('fast', 60)
    _slow = conf.get('slow', 3600)
    _event = Event()
    _track_lock = Lock()
    _LOOP.create_task(tracker())


def fast():
    """Switch to fast polling mode (triggered when a WebSocket client connects)."""
    global _gap
    _gap = _fast
    _LOGS.info("Tracker fast mode")
    if _event:
        _event.set()


def slow():
    """Switch to slow polling mode (triggered when all WebSocket clients disconnect)."""
    global _gap
    _gap = _slow
    _LOGS.info("Tracker slow mode")


async def tracker():
    """Main polling loop: wait for the configured gap, then trigger an update cycle."""
    while True:
        await track()
        try:
            await wait_for(_event.wait(), timeout=_gap)
        except async_timeout:
            pass
        _event.clear()


async def track():
    """Run one update cycle across all objects that have an update() method."""
    async with _track_lock:
        _LOGS.info("Tracker working")
        # Update all devices concurrently rather than sequentially
        tasks = [tracking(obj) for obj in _OBJS if hasattr(obj, 'update')]
        if tasks:
            await gather(*tasks, return_exceptions=True)
        _LOGS.info("Tracker completed")


async def tracking(obj):
    """Update a single object with timeout protection and state broadcast."""
    try:
        r = await wait_for(_do_update(obj), timeout=15)
        if r:
            from .server import send_state
            await send_state(obj if r is True else r)
    except async_timeout:
        _LOGS.error(f"Tracking timeout: {obj.NAME}")
    except Exception as e:
        _LOGS.error(f"Tracking exception: {obj.NAME} {e}")


async def _do_update(obj):
    """Run update(), handling both sync and async, offloading sync to thread pool."""
    _LOGS.debug(f"Tracking {obj.NAME}")
    r = obj.update()
    if iscoroutine(r):
        return await r
    return r