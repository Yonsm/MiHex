"""MQTT module for MiHex.

Subscribes to MQTT topics for sensor data and publishes commands to
control devices. Cached messages enable command templating via
the runner's {{}} expansion syntax.
"""

from json import loads
from paho.mqtt.client import Client, CallbackAPIVersion
from ..server import send_state
from ..entity import Entity, _OBJS, _LOGS, runas

_msgs = {}
_mqtt = None
_MAX_MSGS = 1000         # Message cache limit to prevent unbounded memory growth
_MAX_PAYLOAD = 10000     # Single message payload limit to avoid memory exhaustion


class MQTTClient(Client):
    """Wrapper around paho-mqtt Client with auto-reconnect and topic subscription."""

    def __init__(self, host, port, message_handler, topic='#'):
        super().__init__(CallbackAPIVersion.VERSION2)
        self.on_message = message_handler
        self.on_connect = self._handle_connect
        self.on_connect_fail = lambda c, u: _LOGS.warning("MQTT %s:%s connect failed, retrying..." % (host, port))
        self.on_disconnect = lambda c, u, f, r, p: _LOGS.warning("MQTT %s:%s disconnected: %s" % (host, port, r))
        self.reconnect_delay_set(min_delay=1, max_delay=120)
        self._topic = topic
        self.connect_async(host, port)
        self.loop_start()

    def _handle_connect(self, client, userdata, flags, rc, props):
        if rc == 0:
            self.subscribe(self._topic)
            _LOGS.info("MQTT %s:%s connected" % (self.host, self.port))
        else:
            _LOGS.error("MQTT %s:%s connected error: %s" % (self.host, self.port, rc))


def setup(conf):
    """Initialize the MQTT client and create entity objects from config."""
    global _mqtt
    _mqtt = MQTTClient(conf.get('host', 'localhost'), conf.get('port', 1883), on_message)
    return [MQTTEntity(c, k) for k, v in conf.items() if type(v) is list for c in v]


def service(args):
    """Publish a message or retrieve a cached value by topic.

    - service([topic, payload]) → publish payload to topic
    - service([topic]) → return cached payload (supports dot-notation JSON path)
    """
    argc = len(args)
    if argc > 1:
        _mqtt.publish(args[0], args[1], retain=True)
        return
    elif argc == 1:
        keys = args[0].split('.')
        val = _msgs.get(keys[0])
        if not val:
            return None
        if isinstance(val, bytes):
            val = val.decode()
        return loads(val).get(keys[1], '') if len(keys) > 1 else val


def on_message(client, data, msg):
    """Raw MQTT message callback — validates payload size and dispatches."""
    topic = msg.topic
    payload = msg.payload
    if isinstance(payload, bytes):
        if len(payload) > _MAX_PAYLOAD:
            _LOGS.warning(f"MQTT payload too large ({len(payload)} bytes), dropped: {topic}")
            return
        payload = payload.decode()
    runas(_handle_message(topic, payload))


async def _handle_message(topic, payload):
    """Process an incoming MQTT message: cache it and update matching entities."""
    # Cache the message; evict the oldest entry when the limit is exceeded
    _msgs[topic] = payload
    if len(_msgs) > _MAX_MSGS:
        _msgs.pop(next(iter(_msgs)))

    ents = []
    parsed = None
    for ent in _OBJS:
        if getattr(ent, 'stat', None) == topic:
            json = getattr(ent, 'json', None)
            if json:
                if parsed is None:
                    parsed = loads(payload)
                stat = parsed.get(json)
            else:
                stat = payload
            if getattr(ent, 'STAT', None) != stat:
                ent.STAT = stat
                ents.append(ent)
    if ents:
        await send_state(ents)


class MQTTEntity(Entity):
    """Entity representing an MQTT-controlled device or sensor."""

    def on_turn(self, arg=None):
        topic = self.turn if hasattr(self, 'turn') and arg is not None else self.stat
        payload = not getattr(self, 'STAT', None) if arg is None else arg
        return service([topic, payload])

    def service(self, args):
        return service([self.turn if hasattr(self, 'turn') and args is not None else self.stat, *args])