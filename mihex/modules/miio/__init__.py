"""MiIO module for MiHex.

Integrates Xiaomi/Mi smart home devices via the MiIO protocol.
Supports device discovery, status polling, on/off control, mode
switching, and value tuning (brightness, fan speed, etc.).
"""

from importlib import import_module
from asyncio import gather
from miservice import MiAccount, MiIOService, miio_command
from ...server import send_state
from ...loader import _OBJS, _LOGS
from ...entity import Entity
from .discover import auto_config, get_home_dids, CONF_KEYS
from math import ceil

miio_service = MiIOService()


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

    try:
        miio_service.account = MiAccount(None, conf['user'], conf['pass'])
        devs = await miio_service.device_list('full')
    except Exception as e:
        _LOGS.error('MiIO init failed: %s', e)
        return

    auto = conf.get('auto') or not (k not in CONF_KEYS for k in conf)
    auto_names = set(auto) if isinstance(auto, list) else None
    auto_devs = []

    for dev in devs:
        name = dev['name']
        did = dev['did']

        if name in conf and name not in CONF_KEYS:
            # Manual configuration (existing behavior, takes priority)
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

        elif auto and (auto_names is None or name in auto_names):
            auto_devs.append(dev)

    # Filter by home name if specified (name-list mode already scoped above)
    if auto_devs and not isinstance(auto, list):
        dids = await get_home_dids(miio_service, auto)
        if dids is not None:
            auto_devs = [d for d in auto_devs if d['did'] in dids]

    # Auto-discover in parallel (one entity per device — multiple sensor
    # readings on the same device are merged into DESC, see MiIODevice.update)
    if auto_devs:
        results = await gather(*(auto_config(miio_service, dev['name'], dev.get('model', ''))
                                  for dev in auto_devs))
        for dev, cfg in zip(auto_devs, results):
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
        if not hasattr(self, 'ison') and not hasattr(self, 'stat'):
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
        props = await miio_service.miio_get_props(self.did, iids + [e[1] for e in extra])
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
                        _LOGS.error('MiIO get_prop failed: %s/%s' % (ent.NAME, props[i]))
                    i += 1
            if changed:
                rets.append(ent)

        if extra:
            by_ent = {}
            for ent, iid, unit in extra:
                value = props[i]
                i += 1
                if value is not None:
                    by_ent.setdefault(ent, []).append(f'{value}{unit}')
            for ent, parts in by_ent.items():
                desc = ' '.join(parts)
                if getattr(ent, 'DESC', None) != desc:
                    ent.DESC = desc
                    if ent not in rets:
                        rets.append(ent)

        return rets