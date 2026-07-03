"""Broadlink module for MiHex.

Controls Broadlink IR/RF devices (switches and remotes). Supports
power on/off for smart plugs and IR code transmission for appliances
like air conditioners, projectors, and curtains.
"""

from time import sleep
from yaml import safe_load
from broadlink import gendevice
from base64 import b64decode
from os.path import dirname, abspath, exists, join

from ..entity import Entity, _OBJS, _LOGS, runin


def setup(conf):
    """Initialize Broadlink devices from config and register entities."""
    for dic in conf:
        blrc = gendevice(dic['type'], (dic['host'], 80), bytes.fromhex(dic['mac']), name=dic.get('NAME', 'BLRC'))
        if 'NAME' in dic:
            _OBJS.append(ent := BLSwitchEntity(dic, 'switch'))
            ent._blrc = blrc
            continue
        for k, v in dic.items():
            if type(v) is list:
                for c in v:
                    _OBJS.append(BLRemoteEntity(c, k, blrc))


class BLSwitchEntity(Entity):
    """Entity for a Broadlink smart plug (power on/off)."""

    async def update(self):
        ison = await runin(self._check_power)
        if getattr(self, 'ISON', None) != ison:
            self.ISON = ison
            return True

    def _check_power(self):
        try:
            self._blrc.auth()
            return self._blrc.check_power()
        except Exception as e:
            _LOGS.error('Broadlink check_power failed: %s', e)
            return None

    async def on_turn(self, arg):
        await runin(self._set_power, arg)
        await super().on_turn(arg)

    def _set_power(self, arg):
        try:
            self._blrc.auth()
            self._blrc.set_power(arg)
        except Exception as e:
            _LOGS.error('Broadlink set_power failed: %s', e)


class BLRemoteEntity(Entity):
    """Entity for a Broadlink IR/RF remote control.

    IR codes are loaded from YAML files in the codes/ subdirectory,
    or used as raw base64 strings directly.
    """

    def __init__(self, conf, kind, blrc):
        super().__init__(conf, kind)
        self._blrc = blrc
        file = join(dirname(abspath(__file__)), 'codes', self.code + '.yaml')
        if exists(file):
            with open(file) as f:
                self._cmds = safe_load(f)
        else:
            self._cmds = self.code

    async def service(self, args):
        await runin(self._service, args)

    def _service(self, args):
        cmd = self._cmds
        last = None
        for arg in args:
            last = cmd
            cmd = cmd.get(arg) if isinstance(cmd, dict) else None
        if not isinstance(cmd, str):
            return
        # Support cross-reference: if cmd starts with '=', resolve from parent dict
        if last is not None and len(cmd) > 0 and cmd[0] == '=':
            cmd = last[cmd[1:]]
        # Support repeat syntax: 'code@N' sends the code N times with 1s delay
        parts = cmd.rsplit('@', 1)
        if len(parts) > 1:
            cmd = parts[0]
            repeats = int(parts[1])
        else:
            repeats = 1
        try:
            self._blrc.auth()
            for i in range(repeats):
                if i > 0:
                    sleep(1)
                self._blrc.send_data(b64decode(cmd))
        except Exception as e:
            _LOGS.error('Broadlink send_data failed: %s', e)

    async def on_turn(self, arg):
        await self.service(['打开' if arg else '关闭'])
        await super().on_turn(arg)