"""Viomi washer module for MiHex.

Supports Viomi branded washing machines with appointment scheduling,
mode selection, and MiIO protocol control.
"""

from asyncio import sleep
from importlib import import_module
from datetime import datetime, timedelta
from .. import miio_service, MiIOEntity
from ....loader import _LOGS

APPOINT_MIN = 1  # 3 in app default
APPOINT_MAX = 23  # 19 in app default
DEFAULT_APPOINT_TIME = -8  # -8 means 8 o'clock, 8 means 8 hours later


class viomi_washer(MiIOEntity):
	async def service(self, args):
		try:
			module = import_module('.' + self.model.split('.')[-1], package="mihex.modules.miio.viomi_washer")
		except ImportError:
			_LOGS.warning("洗衣机类型不支持：%s" % self.model)
			return

		argc = len(args)
		if argc > 0:
			if args[0] == '预约取消':
				if hasattr(module, 'Custom'):
					await miio_service.miot_set_prop(self.did, module.Custom.Appoint_Time, 0)
					await sleep(5)
				await miio_service.miot_set_prop(self.did, module.Washer.Switch_Status, False)
				return "预约洗衣已取消"
			elif args[0] != '预约洗衣':
				return await super().service(args)

		await miio_service.miot_set_prop(self.did, module.Washer.Switch_Status, True)
		await sleep(0.5)

		mode = module.Washer_Mode[argc > 1 and args[1] or '黄金洗'].value
		await miio_service.miot_set_prop(self.did, module.Washer.Mode, mode)
		await sleep(1.5)

		atime = argc > 2 and int(args[2]) or DEFAULT_APPOINT_TIME
		now = datetime.now()
		now_stamp = now.timestamp()
		if atime < 0:
			aclock = -atime
			status = '预约%s点钟完成洗衣' % aclock
			hour = now.hour
			if self.model == 'viomi.washer.v13':
				if hour > aclock:
					now += timedelta(days=1)
				stamp = datetime(now.year, now.month, now.day, aclock).timestamp()
				if (stamp - now_stamp) / 3600 > 12:
					atime = 0
			else:
				if now.minute > 10:
					hour += 1
				if hour <= aclock - APPOINT_MIN:
					stamp = aclock - hour
				elif hour >= aclock + 24 - APPOINT_MAX:
					stamp = aclock + 24 - hour
				else:
					stamp = 0
				if stamp > 12:
					atime = 0
		else:
			status = '预约%s小时后完成洗衣' % atime
			stamp = (now_stamp + atime * 60 * 60 * 1000) if self.model == 'viomi.washer.v13' else atime
		if atime:
			if self.model == 'viomi.washer.v5':
				pass
			else:
				await miio_service.miot_set_prop(self.did, module.Custom.Appoint_Time, int(stamp))
		else:
			status = '预约时间%s>12小时，改为立即开始' % stamp
			await miio_service.miot_action(self.did, module.Washer._Start_Wash, [mode])
		return status