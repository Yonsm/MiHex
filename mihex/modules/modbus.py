"""Modbus module for MiHex.

Controls Modbus TCP devices such as HVAC systems via register read/write.
Supports coil, input, and holding register types with configurable data
scaling and offset.
"""

import struct
from ..entity import Entity, runin
from pymodbus.client import ModbusTcpClient
from pymodbus.framer import FramerType
from logging import getLogger, DEBUG
_LOG = getLogger(__name__)

_hub = None
_TYPES = {'int': {1: 'h', 2: 'i', 4: 'q'}, 'uint': {1: 'H', 2: 'I', 4: 'Q'}, 'float': {1: 'e', 2: 'f', 4: 'd'}}


def setup(conf):
    global _hub
    _hub = ModbusTcpClient(host=conf['host'], port=conf.get('port', 8899), framer=FramerType.RTU)
    return [ModbusEntity(c, conf.get('mods'), conf.get('fan_mods')) for c in conf['climate']]


class ModbusEntity(Entity):

    def __init__(self, conf, mods, fan_mods):
        super().__init__(conf, 'climate')
        self.UNIT = '℃'
        self.TOGG = '▽'
        self.TUNE = '△'
        self._mods = mods
        self._fan_mods = fan_mods
        self._error = 0
        for prop in ['turn', 'temp', 'tune', 'mode', 'fan_mode']:
            if not hasattr(self, prop):
                continue
            reg = getattr(self, prop)
            if type(reg) is not dict:
                reg = {'reg': reg}
                setattr(self, prop, reg)
            count = reg.get('count', 1)
            dtype = reg.get('dtype')
            if dtype != 'custom':
                try:
                    reg['structure'] = '>{}'.format(_TYPES['int' if dtype is None else dtype][count])
                except KeyError:
                    _LOG.error("Unable to detect data type for %s", prop, exc_info=_LOG.isEnabledFor(DEBUG))
                    continue

    @property
    def MODS(self):
        return list(self._mods.values())

    def reset(self):
        _LOG.warning("Reset")
        import socket
        import time
        s = socket.socket(socket.AF_INET, socket.SOCK_STREAM)
        s.settimeout(5)
        s.connect((_hub.comm_params.host, _hub.comm_params.port))
        s.sendall(b'\x55\xAA\x55\x00\x25\x80\x03\xA8')
        s.close()
        time.sleep(1)

    def reconn(self):
        _LOG.warning("Reconnect")

    def exception(self):
        turns = self._error // 4
        self._error += 1
        if turns != 0 and (turns > 6 and turns % 10):
            return
        if turns % 3 == 0:
            self.reset()
        self.reconn()

    async def update(self):
        try:
            result = await runin(self._read_all)
            if result:
                return True
        except Exception as e:
            self.STAT = None
            self.DESC = None
            self.ISON = 0
            self.MODE = None
            _LOG.error("Read exception: %s", e, exc_info=_LOG.isEnabledFor(DEBUG))
            await runin(self.exception)

    def _read_all(self):
        """Read all registers synchronously (runs in thread pool executor)."""
        self.STAT = self.read(self.temp)
        self.DESC = self.read(self.tune)
        self.ISON = self.read(self.turn)
        mode_val = self.read(self.mode)
        self.MODE = self._mods.get(mode_val) if mode_val is not None else None
        return True

    async def on_turn(self, arg):
        await runin(self.write, self.turn, arg)
        await super().on_turn(arg)

    async def on_mode(self, arg):
        mode = next((k for k, v in self._mods.items() if v == arg), None)
        await runin(self.write, self.mode, mode)
        # self.MODE = arg
        # await send_state(self)

    async def on_tune(self, arg):
        await runin(self.write, self.tune, arg)
        # self.DESC = arg
        # await send_state(self)

    def register(self, reg):
        rtype = reg.get('type', 'holding')
        address = reg.get('reg')
        slave = reg.get('slave', 1)
        scale = reg.get('scale', 1)
        offset = reg.get('offset', 0)
        count = reg.get('count', 1)
        return (rtype, slave, address, scale, offset, count)

    def read(self, reg):
        rtype, slave, address, scale, offset, count = self.register(reg)
        if rtype == 'coil':
            ret = _hub.read_coils(address, slave=slave, count=count)
            if ret.isError():
                _LOG.error("read_coils error: %s", ret)
                return None
            return bool(ret.bits[0])
        if rtype == 'input':
            ret = _hub.read_input_registers(address, slave=slave, count=count)
        else:
            ret = _hub.read_holding_registers(address, slave=slave, count=count)
        if ret.isError():
            _LOG.error("read_registers error: %s", ret)
            return None
        registers = ret.registers
        if reg.get('reverse'):
            registers.reverse()
        data = b''.join([x.to_bytes(2, byteorder='big') for x in registers])
        val = struct.unpack(reg['structure'], data)[0]
        return scale * val + offset

    def write(self, reg, value):
        rtype, slave, address, scale, offset, count = self.register(reg)
        if rtype == 'coil':
            _hub.write_coil(address, bool(value), slave=slave)
        else:
            val = (value - offset) / scale
            _hub.write_register(address, int(val), slave=slave)