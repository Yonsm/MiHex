"""MiIO module for MiHex.

Integrates Xiaomi/Mi smart home devices via the MiIO protocol.
Supports device discovery, status polling, on/off control, mode
switching, and value tuning (brightness, fan speed, etc.).
"""

from importlib import import_module
from asyncio import gather, sleep, Semaphore
from miservice import MiAccount, MiIOService, miio_command
from ...server import send_state
from ...loader import _OBJS
from ...entity import Entity
from .discover import auto_config, get_home_dids, group_members, CONF_KEYS
from math import ceil
from logging import getLogger, DEBUG
_LOG = getLogger(__name__)

miio_service = MiIOService()

# Startup retries for the initial cloud login/device_list fetch — mihex often
# starts before the router's WAN link is fully up, so a transient failure
# here shouldn't strand the whole miio module until the next restart.
_INIT_RETRIES = 3
_INIT_BACKOFF = 2

# Cap concurrent MIoT spec fetches during auto-discovery so a large device
# list doesn't fire dozens of simultaneous requests at miot-spec.org.
_DISCOVER_CONCURRENCY = 5


def twins_split(string, default=None, sep='-'):
    """Parse a 'start-end' or single-value string into a (int, int) tuple.

    - '3'     → (3, default)
    - '2-5'   → (2, 5)
    """
    pos = string.find(sep)
    return (int(string), default) if pos == -1 else (int(string[0:pos]), int(string[pos + 1:]))


async def setup(conf):
    """Authenticate with Xiaomi cloud and register configured devices.

    Config format:
        miio:
          user: xxx
          pass: xxx
          auto: true              # Auto-discover every device in every home
          # auto: 我的家            # ...or only devices from one home
          # auto: [台灯, 新风机]    # ...or only these specific device names
          手动设备名:              # Manual config (takes priority over auto)
            TYPE: light
            ICON: lamp
    """

    miio_service.account = MiAccount(None, conf['user'], conf['pass'])
    devs = None
    for attempt in range(1, _INIT_RETRIES + 1):
        try:
            devs = await miio_service.device_list('full')
            break
        except Exception as e:
            _LOG.error("Init failed (%d/%d): %s", attempt, _INIT_RETRIES, e, exc_info=_LOG.isEnabledFor(DEBUG))
            if attempt < _INIT_RETRIES:
                await sleep(_INIT_BACKOFF * attempt)
    if devs is None:
        return

    auto = conf.get('auto') or all(k in CONF_KEYS for k in conf)
    auto_names = set(auto) if isinstance(auto, list) else None
    auto_devs = []
    absorbed = group_members(devs)
    manual_names = {k for k in conf if k not in CONF_KEYS}

    for dev in devs:
        name = dev['name']
        did = dev['did']

        if name in conf and name not in CONF_KEYS:
            # Manual configuration (existing behavior, takes priority)
            manual_names.discard(name)
            args = conf[name]
            if type(args) is str:
                args = {'TYPE': args}
            if type(args) is dict:
                args = [args]
            ents = []
            for i in range(len(args)):
                c = args[i]
                if platform := c.get('platform'):
                    cls = getattr(import_module('.' + platform, package="mihex.modules.miio"), platform)
                else:
                    cls = MiIODevice if i == 0 else MiIOEntity
                ents.append(cls(c, did, name))
            ents[0].ents = ents
            _OBJS.extend(ents)

        elif auto and did not in absorbed and (auto_names is None or name in auto_names):
            auto_devs.append(dev)

    for name in manual_names:
        _LOG.warning("Configured device \"%s\" not found in Mi Home account (renamed or removed?)", name)

    # Filter by home name if specified (name-list mode already scoped above)
    if auto_devs and not isinstance(auto, list):
        dids = await get_home_dids(miio_service, auto)
        if dids is not None:
            auto_devs = [d for d in auto_devs if d['did'] in dids]

    # Auto-discover in parallel, capped at _DISCOVER_CONCURRENCY concurrent
    # spec fetches (one entity per device — multiple sensor readings on the
    # same device are merged into DESC, see MiIODevice.update)
    if auto_devs:
        sem = Semaphore(_DISCOVER_CONCURRENCY)

        async def _auto_config(dev):
            async with sem:
                return await auto_config(miio_service, dev['name'], dev.get('model', ''))

        results = await gather(*(_auto_config(dev) for dev in auto_devs))
        for dev, cfg in zip(auto_devs, results):
            if cfg is None:
                continue
            ent = MiIODevice(cfg, dev['did'], dev['name'])
            ent.ents = [ent]
            _OBJS.append(ent)


_ATTRS = ['stat', 'ison', 'mode', 'tune', 'togl', 'togr']


class MiIOEntity(Entity):
    """Base entity for a MiIO device property or sub-device."""

    def __init__(self, conf, did, name):
        super().__init__(conf, 'sensor')
        self.did = did
        if not hasattr(self, 'NAME'):
            self.NAME = name
        for k in _ATTRS:
            if v := getattr(self, k, None):
                if v[0].isdigit():
                    setattr(self, k, twins_split(v, 1))
        if extra := getattr(self, 'extra_stat', None):
            self.extra_stat = [(twins_split(iid, 1) if iid[0].isdigit() else iid, unit) for iid, unit in extra]
        if not hasattr(self, 'ison') and not hasattr(self, 'stat') and self.TYPE != 'binary':
            self.ison = (2, 1)

    async def service(self, args):
        """Execute a raw MiIO command on this device."""
        r = await miio_command(miio_service, self.did, ' '.join(args))
        return r[0] if isinstance(r, list) and len(r) == 1 else r

    async def on_turn(self, ison):
        """Turn the device on or off."""
        voon = getattr(self, 'voon' if ison else 'voff', ison)
        await miio_service.miio_set_prop(self.did, self.ison, voon)
        await super().on_turn(ison)

    async def on_mode(self, mode):
        """Switch the device operating mode."""
        await miio_service.miio_set_prop(self.did, self.mode, mode)

    async def on_tune(self, tune):
        """Handle tune adjustments: increment/decrement, toggle, or rotate."""
        if tune == '▽' or tune == '△':
            min = getattr(self, 'tune_min', 0)
            max = getattr(self, 'tune_max', 100)
            step = getattr(self, 'tune_step', ceil((max - min) / 20))
            val = self.TUNE + (step if tune == '△' else -step)
            if val < min:
                val = min
            elif val > max:
                val = max
            await miio_service.miio_set_prop(self.did, getattr(self, 'tune_set', self.tune), val)
            self.TUNE = val
        elif tune == '◉' or tune == '◌':
            TOGL_ON = getattr(self, 'TOGL_ON', 'on')
            val = getattr(self, 'TOGL_OFF', 'off') if self.TOGL == TOGL_ON else TOGL_ON
            await miio_service.miio_set_prop(self.did, self.togl, val)
            self.TOGL = val
        elif tune == '◑' or tune == '◐':
            TOGR_ON = getattr(self, 'TOGR_ON', 'on')
            val = getattr(self, 'TOGR_OFF', 'off') if self.TOGR == TOGR_ON else TOGR_ON
            await miio_service.miio_set_prop(self.did, self.togr, val)
            self.TOGR = val
        await send_state(self)


class MiIODevice(MiIOEntity):
    """Primary entity for a MiIO device; polls all sub-entity states."""

    async def update(self):
        """Fetch all property values from the device and update sub-entities.

        Any `extra_stat` readings (extra sensor values found by auto-discovery
        alongside the primary `stat`, e.g. temperature *and* humidity *and*
        PM2.5 on the same device) are merged into a single DESC summary
        string instead of becoming separate entities.
        """
        rets = []
        iids = [v for ent in self.ents for attr in _ATTRS if (v := getattr(ent, attr, None))]
        extra = [(ent, iid, unit) for ent in self.ents for iid, unit in getattr(ent, 'extra_stat', [])]
        try:
            props = await miio_service.miio_get_props(self.did, iids + [e[1] for e in extra])
        except Exception as e:
            # Whole request failed (cloud/network down) — clear STAT/ISON so the
            # dashboard shows the device as offline instead of stale data.
            _LOG.error("get_props failed: %s %s", self.NAME, e, exc_info=_LOG.isEnabledFor(DEBUG))
            for ent in self.ents:
                for attr in ('STAT', 'ISON'):
                    if getattr(ent, attr, None) is not None:
                        setattr(ent, attr, None)
                        if ent not in rets:
                            rets.append(ent)
            return rets
        i = 0
        for ent in self.ents:
            changed = False
            for attr in _ATTRS:
                if hasattr(ent, attr):
                    value = props[i]
                    if iids[i] == 'temp_dec':
                        value /= 10
                    if value is not None:
                        ATTR = attr.upper()
                        if getattr(ent, ATTR, None) != value:
                            setattr(ent, ATTR, value)
                            changed = True
                    else:
                        _LOG.error("get_prop failed: %s/%s", ent.NAME, props[i])
                    i += 1
            if changed:
                rets.append(ent)

        if extra:
            by_ent = {}
            for ent, iid, unit in extra:
                value = props[i]
                i += 1
                if value is not None:
                    if unit == '℃':
                        value = f'{value:.1f}'
                    by_ent.setdefault(ent, []).append(f'{value}{unit}')
            for ent, parts in by_ent.items():
                desc = ' '.join(parts)
                if getattr(ent, 'DESC', None) != desc:
                    ent.DESC = desc
                    if ent not in rets:
                        rets.append(ent)

        return rets