"""DingTalk (Ding) notification module for MiHex.

Sends text messages to a DingTalk group via a custom robot webhook,
supporting HMAC-SHA256 signed requests for secure authentication.
"""

from ..entity import _LOGS
from ..runner import fetch

_token = None
_secret = None


async def service(args):
    """Send a text message to the DingTalk group.

    Automatically signs the request if a secret is configured.
    """
    url = "https://oapi.dingtalk.com/robot/send?access_token=" + _token
    if _secret:
        import time
        import hmac
        import hashlib
        import base64
        from urllib.parse import quote_plus
        timestamp = round(time.time() * 1000)
        hmac_code = hmac.new(_secret.encode(), '{}\n{}'.format(timestamp, _secret).encode(), digestmod=hashlib.sha256).digest()
        sign = quote_plus(base64.b64encode(hmac_code))
        url += '&timestamp=' + str(timestamp) + '&sign=' + sign
    resp = await fetch(url, {'msgtype': 'text', 'text': {'content': args[-1]}})
    if resp['errcode'] == 0:
        return resp
    _LOGS.error('DingTalk send failed: %s', resp)


def setup(conf):
    """Initialize DingTalk token and secret from config."""
    global _token, _secret
    _token = conf['token']
    _secret = conf.get('secret')