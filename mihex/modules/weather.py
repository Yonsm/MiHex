"""Weather module for MiHex.

Fetches real-time weather data from the Caiyun (ColorfulClouds) Weather API,
including temperature, humidity, PM2.5, and forecast descriptions.
Data is cached for 1 hour to reduce API calls.
"""

from random import randint
from time import time
from ..entity import Entity, _LOGS
from ..runner import fetch

_API_KEY = 'UR8ASaplvIwavDfR'

_ICONS = {
    'CLEAR_NIGHT': 'night',
    'CLOUDY': 'cloudy',
    'FOG': 'fog',
    'HAIL': 'hail',
    'PARTLY_CLOUDY_DAY': 'partly-cloudy',
    'PARTLY_CLOUDY_NIGHT': 'night-partly-cloudy',
    'RAIN': 'rainy',
    'SNOW': 'snowy',
    'SLEET': 'snowy-rainy',
    'CLEAR_DAY': 'sunny',
    'WIND': 'windy',
    'HAZE': 'windy-variant',
}


async def fetch2(location):
    """Fetch weather data from the Caiyun Weather API.

    Reference: https://open.caiyunapp.com/实况天气接口/v2.2
    """
    did = '5F544F93-44F1-43C9-94B2-%012X' % randint(0, 0xffffffffffff)
    url = f"http://api.caiyunapp.com/v2/{_API_KEY}/{location}/weather?lang=zh_CN&tzshift=28800&timestamp={int(time())}&hourlysteps=384&dailysteps=16&alert=true&device_id={did}"
    resp = await fetch(url, headers={'User-Agent': 'ColorfulCloudsPro/3.2.2 (iPhone; iOS 11.3; Scale/3.00)', 'Accept': 'application/json', 'Accept-Language': 'zh-Hans-CN;q=1'})
    if resp['status'] != 'ok':
        raise Exception(resp)
    return resp


class WeatherEntity(Entity):
    """Entity representing the current weather at a configured location."""

    def __init__(self, data):
        super().__init__(data, 'weather')
        self.NAME = '天气'
        self.ICON = 'weather-cloudy-alert'
        self.STAT = None
        self.DESC = None
        self.LINK = 'https://h5.caiyunapp.com/h5'
        self.stamp = 0

    async def update(self):
        """Refresh weather data if the cache has expired (1 hour)."""
        if self.STAT and int(time()) - self.stamp < 3600:
            return _LOGS.debug("Weather data is fresh")
        if resp := await fetch2(self.location):
            result = resp['result']
            realtime = result['realtime']
            skycon = realtime['skycon']
            temperature = round(realtime['temperature'])
            humidity = int(realtime['humidity'] * 100)
            icon = 'weather-' + (_ICONS[skycon] if skycon in _ICONS else 'cloudy-alert')
            stat = 1
            desc = f"{temperature}℃ {humidity}% {realtime.get('pm25')}μg {result['forecast_keypoint']}"
            self.stamp = resp['server_time']
            _LOGS.debug("Weather data updated")
        else:
            icon = 'weather-cloudy-alert'
            stat = None
            desc = None
        if self.ICON != icon or self.STAT != stat or self.DESC != desc:
            self.ICON = icon
            self.STAT = stat
            self.DESC = desc
            return True

    async def service(self, args=None):
        """Return the current weather description string."""
        await self.update()
        return self.DESC


async def setup(conf):
    """Initialize the weather entity with an optional custom API key."""
    global _API_KEY
    _API_KEY = conf.get('api_key', _API_KEY)
    return WeatherEntity(conf)