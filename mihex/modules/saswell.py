
"""Saswell thermostat module for MiHex.

Communicates with Saswell underfloor heating thermostats via TCP.
Supports status polling, on/off control, and temperature setpoint adjustment.
"""

from ..entity import Entity
from asyncio import open_connection, sleep
from logging import getLogger, DEBUG
_LOG = getLogger(__name__)


async def setup(conf):
	return [SaswellEntity(c) for c in conf]


class SaswellEntity(Entity):

	def __init__(self, conf):
		super().__init__(conf, 'thermostat')
		self.attrs = {}
		self.UNIT = '℃'
		if not hasattr(self, 'port'):
			self.port = 2000
		if not hasattr(self, 'did'):
			self.did = None
		if not hasattr(self, 'iid'):
			self.iid = '1'
		if not hasattr(self, 'stat'):
			self.stat = 'S00'
		if not hasattr(self, 'turn'):
			self.turn = 'S01'
		if not hasattr(self, 'tune'):
			self.tune = 'S02'
		# if not hasattr(self, 'away'):
		# 	self.away = 'S03'
		self.TOGG = '▽'
		self.TUNE = '△'

	async def update(self, command=None):
		try:
			reader, writer = await open_connection(self.host, self.port)
			if command is not None:
				_LOG.debug("Send command: %s", command)
				await reader.read(1024)  # Receive dummy
				writer.write(command.encode())
				await sleep(0.3)
			if self.did is not None:
				polling = f'/{self.did}/{self.stat}/{self.iid}\n'
				_LOG.debug("Send polling: %s", polling)
				writer.write(polling.encode())
			while chunk := await reader.read(1024):
				lines = chunk.decode().split('\n')
				for line in lines:
					if len(line) < 1 or line[0] != '/':
						# _LOG.error("Exception topic: " + line)
						continue
					parts = line[1:].split('/')
					if self.did is None:
						self.did = parts[0]
					elif self.did != parts[0]:
						_LOG.error("Device mis-match: %s", line)
						continue
					if len(parts) < 4 or parts[1] != self.stat or '/'.join(parts[2:-1]) != self.iid:
						_LOG.debug("Not status topic: %s", line)
						continue
					status = parts[-1].split(',')
					if len(status) < 11:
						_LOG.error("Exception status: %s", line)
						continue
					_LOG.debug("Status: %s", line)
					# self.attrs['开关'] = '开启' if status[0] == '1' else '关闭'
					# self.attrs['温度'] = float(status[1])
					# self.attrs['目标温度'] = float(status[2])
					# self.attrs['离家模式'] = '离家' if status[4] == '1' else '在家'
					# self.attrs['最低温度'] = float(status[9])
					# self.attrs['最高温度'] = float(status[10])
					self.ISON = int(status[0])
					self.STAT = status[1]
					self.DESC = (status[2][:-2] if status[2].endswith('.0') else status[2]) + '℃'
					from ..server import send_state
					await send_state(self)
		except Exception as e:
			_LOG.error("Update failed: %s", e, exc_info=_LOG.isEnabledFor(DEBUG))
		finally:
			if 'writer' in locals():
				writer.close()
				await writer.wait_closed()

	async def control(self, sid, val):
		if not self.did:
			return "Device not ready"
		await self.update(f'/{self.did}/{sid}/{self.iid}/{val}\n')

	async def on_turn(self, arg):
		await super().on_turn(arg)
		return await self.control(self.turn, arg)

	async def on_tune(self, arg):
		return await self.control(self.tune, arg)
