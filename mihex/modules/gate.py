"""Mi Smart Home LAN Gateway module for MiHex.

Listens for voice commands from a Xiaomi LAN gateway via MQTT and
translates them into device control actions using MiHex's command
dispatch system. Supports Chinese word substitutions for natural
voice input parsing.
"""

from ..entity import runas
from ..runner import exec
from .mqtt import MQTTClient, loads
from logging import getLogger, DEBUG
_LOG = getLogger(__name__)

# Word substitutions for voice command parsing (Chinese → separator symbols)
_SUBS = {'文本': '#', '设为': '=', '也': ',', '者': '/', '乎': '.'}


def on_message(client, userdata, message):
    """Handle incoming MQTT messages from the gateway.

    Parses the JSON payload to extract voice command text, applies
    word substitutions, and dispatches the command for execution.
    """
    try:
        payload = message.payload
        sep = payload.find(b'{')
        if sep != -1:
            payload = payload[sep:payload.rfind(b'}') + 1]
            body = loads(payload.decode())
            params = body.get('params')
            if not isinstance(params, dict) or params.get('siid') != 7 or params.get('eiid') != 1 or body.get('method') != 'event_occured':
                return
            value = params['arguments'][0]['value']
        else:
            value = payload.decode()
        for old, new in _SUBS.items():
            value = value.replace(old, new)
        runas(exec(value.split('之')))
    except Exception as e:
        _LOG.error("Command failed: %s", e, exc_info=_LOG.isEnabledFor(DEBUG))


def setup(conf):
    """Connect to the Xiaomi LAN gateway MQTT broker."""
    MQTTClient(conf.get('host', 'localhost'), conf.get('port', 8884), on_message, 'local/lan/devMsg')