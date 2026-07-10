"""MIoT auto-discovery for MiHex.

When 'auto' is set in the miio config, unconfigured devices are
automatically discovered and registered as single entities:

    auto: true                  → all devices from all homes
    auto: 我的家                 → only devices from the "我的家" home
    auto: [台灯, 新风机]          → only devices whose Mi Home name is listed

Each discovered device becomes a single Entity with NAME set to the Mi
Home device name. The MIoT spec is used to auto-detect control attributes
(on/off, mode, tune) and TYPE (via the spec's own service type, which is
brand-agnostic and far more reliable than guessing from the model string).
Multiple sensor readings on the same device (e.g. temperature + humidity
+ PM2.5) are merged into a single DESC summary string rather than split
into separate entities, so one physical device always maps to one card.
"""

from json import load, dump
from os import fdopen, replace
from tempfile import mkstemp
from logging import getLogger
_LOG = getLogger(__name__)

# Local cache of MIoT specs (keyed by model), so repeated mihex restarts don't
# re-fetch the spec for every already-seen device from miot-spec.org. Specs
# are effectively immutable once published, so no TTL/invalidation is needed
# — delete the file to force a refresh. Path is relative to the config dir
# (mihex chdir's there at startup), matching miservice's own '.mi.token'.
_SPEC_CACHE_FILE = '.miot_specs.json'
_spec_cache = None


def _spec_cache_get(model):
    """Return the cached spec dict for a model, or None if not cached."""
    global _spec_cache
    if _spec_cache is None:
        try:
            with open(_SPEC_CACHE_FILE) as f:
                _spec_cache = load(f)
        except (OSError, ValueError):
            _spec_cache = {}
    return _spec_cache.get(model)


def _spec_cache_put(model, spec):
    """Store a spec dict for a model and persist the cache to disk."""
    _spec_cache[model] = spec
    try:
        fd, tmp = mkstemp(dir='.')
        with fdopen(fd, 'w') as f:
            dump(_spec_cache, f)
        replace(tmp, _SPEC_CACHE_FILE)
    except OSError as e:
        _LOG.warning("Failed to save MIoT spec cache: %s", e)

# Config keys in miio section that are NOT device names
CONF_KEYS = frozenset({'user', 'pass', 'auto'})

# MIoT service-type URN segment → TYPE. This is part of the open MIoT spec
# (urn:miot-spec-v2:service:<name>:...) so it works across every brand,
# unlike guessing from Xiaomi's internal model string.
_SERVICE_TYPES = {
    'light': 'light',
    'outlet': 'switch',
    'switch': 'switch',
    'fan': 'fan',
    'fan-control': 'fan',
    'ceiling-fan': 'fan',
    'air-purifier': 'purifier',
    'air-fresh-ventilator': 'purifier',
    'air-freshener': 'purifier',
    'air-condition-outlet': 'climate',
    'air-conditioner': 'climate',
    'air-condition': 'climate',
    'heater': 'climate',
    'ptc-bath-heater': 'fan',       # 浴霸 (heat lamp + ventilation fan combo)
    'water-heater': 'climate',
    'thermostat': 'thermostat',     # 地暖
    'vacuum': 'vacuum',
    'washer': 'washer',
    'curtain': 'cover',
    'window-opener': 'cover',
    'camera': 'camera',              # device-level type fallback (see auto_config)
    'camera-control': 'camera',
    'humidifier': 'humidifier',
    'dehumidifier': 'humidifier',
    'magnet-sensor': 'binary',      # 门窗传感器
    'motion-sensor': 'binary',      # 人体传感器 (PIR)
    'occupancy-sensor': 'binary',   # 人在传感器 (mmWave 存在感应)
    'submersion-sensor': 'binary',
    'smoke-sensor': 'binary',
    'electronic-valve': 'switch',   # 灯闸/插闸/电表总闸 (智能断路器)
    'television': 'switch',
    'air-fryer': 'switch',
    'microwave-oven': 'switch',
}

# Model prefix → TYPE, used only as a fallback when the MIoT spec fetch fails
MODEL_TYPES = {
    'yeelink.light': 'light',
    'zhimi.airpurifier': 'purifier',
    'zhimi.fan': 'fan',
    'zhimi.humidifier': 'humidifier',
    'chuangmi.plug': 'switch',
    'chuangmi.camera': 'camera',
    'roborock.vacuum': 'vacuum',
    'xiaomi.aircondition': 'climate',
    'xiaomi.airp': 'purifier',
    'xiaomi.fan': 'fan',
    'xiaomi.humidifier': 'humidifier',
    'cuco.plug': 'switch',
    'qmi.powerstrip': 'switch',
    'zimi.powerstrip': 'switch',
    'nwt.derh': 'humidifier',
    'deerma.humidifier': 'humidifier',
    'leshow.fan': 'fan',
    'shhf.light': 'light',
}

# MIoT spec URN suffix → MiHex attr (controls only)
_SPEC_ATTRS = {
    'on': 'ison',
    'is-on': 'ison',        # TV power state (xiaomi-spec, vendor-specific urn)
    'valve-switch': 'ison', # electronic-valve on/off (灯闸/插闸/电表总闸)
    'mode': 'mode',
    'fan-level': 'tune',
    'brightness': 'tune',
    'target-temperature': 'tune',
    'color-temperature': 'tune',
    'wind-level': 'tune',
    'speed-level': 'tune',
    'volume': 'tune',
}

# MIoT spec URN suffix → display unit (sensor stats; also doubles as priority
# order when picking which reading becomes the tile's headline STAT/UNIT).
# battery-level is deliberately not tracked — it's on nearly every device and
# not worth polling or displaying.
_SPEC_STATS = {
    'tds-out': 'tds',
    'co2-density': 'ppm',
    'temperature': '℃',
    'relative-humidity': '%',
    'pm2.5-density': 'aqi',
    'aqi': 'aqi',
    'illumination': 'lx',
}

_SPEC_UNIT = {
    'celsius': '℃', 'fahrenheit': '℉',
    'percentage': '%',
    'μg/m3': 'aqi', 'μg/m³': 'aqi',
    'lux': 'lx', 'ppm': 'ppm',
}

# Binary service name (see _SERVICE_TYPES) → mdi icon. Binary sensors always
# show a fixed icon instead of their stat text, so unlike regular sensors
# each sub-type gets its own icon rather than sharing one.
_BINARY_ICONS = {
    'magnet-sensor': 'door',
    'motion-sensor': 'motion-sensor',
    'occupancy-sensor': 'radar',
    'submersion-sensor': 'water',
    'smoke-sensor': 'smoke-detector',
}

# Service name → mdi icon, for single-purpose appliances that would otherwise
# share the generic icon of their assigned TYPE (e.g. every 'switch' device
# defaults to a plain light-switch icon in dash.js).
_TYPE_ICONS = {
    'router': 'router-wireless',
    'television': 'television',
    'electronic-valve': 'fuse',
    'air-fryer': 'pot-steam',
    'microwave-oven': 'microwave',
}

# Device (top-level) type URN → skip auto-discovery entirely. These are
# network/voice/remote accessories with no single meaningful state worth a
# tile — surfacing them as a generic 'sensor' card is just noise.
_SKIP_DEVICE_TYPES = {
    'repeater',        # WiFi 放大器/中继器
    'speaker',         # 小爱同学音箱
    'microphone',      # 独立麦克风
    'remote-control',  # 蓝牙场景开关/遥控器
    'rangefinder',     # IR/蓝牙遥控器 (giot.etool 系列)
}

# Extra sensor readings are joined into DESC in this priority order,
# regardless of the order they appear in the device's spec.
_DESC_ORDER = ['pm2.5-density', 'temperature', 'relative-humidity']

# Services that are never the "main" control of a device, so their
# properties are excluded from control/stat extraction entirely.
_IGNORED_SERVICES = {'indicator-light'}


def guess_type(model):
    """Guess MiHex TYPE from Xiaomi device model name (fallback only)."""
    for prefix, type_ in MODEL_TYPES.items():
        if model.startswith(prefix):
            return type_
    return None


def _urn_name(urn):
    """Extract the semantic name from a MIoT type URN, e.g.

    'urn:miot-spec-v2:property:on:00000006:zhimi-v1:1' → 'on'
    'urn:miot-spec-v2:service:fan:00007810:xiaomi-v1:1' → 'fan'

    The name always sits at a fixed position (4th colon-separated segment),
    not at the end — the last segment is a revision number.
    """
    parts = urn.split(':')
    return parts[3] if len(parts) > 3 else ''


def parse_spec(spec):
    """Parse MIoT spec to auto-detect device attributes (flat conf dict).

    Extracts control attributes (ison, mode, tune) and every sensor stat
    exposed by the device. The highest-priority stat (by _SPEC_STATS order)
    becomes the primary `stat`/`UNIT`; any remaining stats are kept as
    `extra_stat` (a list of (iid, unit) pairs) so they can be merged into a
    single DESC summary string instead of being dropped or split into
    separate entities.

    Returns (conf, type_hint) — type_hint is the TYPE guessed from the
    spec's own service type (None if no known service was found).
    """
    services = [svc for svc in spec.get('services', [])
                if svc.get('description', '').lower() != 'device information']

    # Resolve TYPE first, with 'light' deferred to lowest priority: a light+
    # fan combo device (灯扇/浴霸) exposes both a 'light' service and a
    # 'fan'/'ceiling-fan'/'ptc-bath-heater' service, and should classify by
    # the latter, not by the bulb that happens to be listed first.
    type_hint = None
    light_hint = None
    for svc in services:
        svc_type = _SERVICE_TYPES.get(_urn_name(svc.get('type', '')))
        if svc_type == 'light':
            if light_hint is None:
                light_hint = svc_type
        elif svc_type is not None and type_hint is None:
            type_hint = svc_type
    if type_hint is None:
        type_hint = light_hint
    skip_light = type_hint is not None and type_hint != 'light'

    conf = {}
    stats = []  # (urn, iid, unit) candidates, in encounter order

    for svc in services:
        siid = svc.get('iid', 0)
        svc_name = _urn_name(svc.get('type', ''))
        svc_type = _SERVICE_TYPES.get(svc_name)

        if svc_name in _IGNORED_SERVICES or (skip_light and svc_type == 'light'):
            continue

        if svc_name in _TYPE_ICONS and 'ICON' not in conf:
            conf['ICON'] = _TYPE_ICONS[svc_name]

        for prop in svc.get('properties', []):
            piid = prop.get('iid', 0)
            iid = f'{siid}-{piid}'
            access = prop.get('access', [])
            urn = _urn_name(prop.get('type', ''))

            # Binary security sensors (door/window, motion, water leak, smoke)
            # each expose a single read-only bool status property, but its
            # URN name isn't standardized across brands the way climate/light
            # controls are — so rely on the service's own classification
            # instead of a name lookup, and take the first bool reading.
            # Occupancy sensors are the one exception: 'occupancy-status' IS
            # a standardized property name, but brands report it as a uint8/
            # int8 enum (0 = no one, nonzero = occupied/count) instead of a
            # bool — still works as ISON since JS treats 0 as falsy and any
            # nonzero reading as truthy.
            if svc_type == 'binary' and 'ison' not in conf and 'read' in access \
                    and (prop.get('format') == 'bool' or urn == 'occupancy-status'):
                conf['ison'] = iid
                conf['ICON'] = _BINARY_ICONS.get(svc_name, 'bullseye')
                continue

            # Control attributes
            if urn in _SPEC_ATTRS and 'write' in access:
                attr = _SPEC_ATTRS[urn]

                if attr == 'ison' and 'ison' not in conf:
                    conf['ison'] = iid
                    for v in (prop.get('value-list') or []):
                        vd = v.get('description', '').lower()
                        if vd in ('on', '开启', 'open', 'true'):
                            conf['voon'] = v['value']
                        elif vd in ('off', '关闭', 'close', 'false'):
                            conf['voff'] = v['value']

                elif attr == 'mode' and 'mode' not in conf:
                    conf['mode'] = iid
                    vlist = prop.get('value-list')
                    if vlist:
                        conf['MODS'] = {v['description']: v['value']
                                        for v in vlist if v.get('description')}

                elif attr == 'tune' and 'tune' not in conf:
                    conf['tune'] = iid
                    vrange = prop.get('value-range')
                    if vrange:
                        conf['tune_min'] = vrange[0]
                        conf['tune_max'] = vrange[1]
                        if len(vrange) > 2 and vrange[2] != 1:
                            conf['tune_step'] = vrange[2]

            # Sensor stat candidates (collect all, not just the first match)
            elif urn in _SPEC_STATS and 'read' in access and 'write' not in access:
                stats.append((urn, iid, prop.get('unit', '')))

    # Primary stat = highest-priority reading; the rest become extra_stat.
    # Binary sensors (door/window, motion, occupancy) always show a fixed
    # icon instead of the stat text, so `stat` would never be rendered —
    # every reading (e.g. illumination) goes to extra_stat/DESC instead.
    primary = None
    if type_hint != 'binary':
        for stat_urn in _SPEC_STATS:
            primary = next((s for s in stats if s[0] == stat_urn), None)
            if primary:
                break

    if primary:
        conf['stat'] = primary[1]
        conf['UNIT'] = _SPEC_UNIT.get(primary[2].lower(), _SPEC_STATS[primary[0]])

    # Remaining readings become extra_stat/DESC, in a fixed priority order
    # rather than spec encounter order, and excluding any reading that shares
    # the primary's unit (e.g. a vendor-custom duplicate of the primary stat).
    extras = [(urn, iid, _SPEC_UNIT.get(unit.lower(), _SPEC_STATS[urn]))
              for urn, iid, unit in stats if (urn, iid, unit) != primary]
    extras = [e for e in extras if e[2] != conf.get('UNIT')]
    extras.sort(key=lambda e: _DESC_ORDER.index(e[0]) if e[0] in _DESC_ORDER else len(_DESC_ORDER))
    if extras:
        conf['extra_stat'] = [(iid, unit) for _, iid, unit in extras]

    return conf, type_hint


async def auto_config(miio_service, name, model):
    """Auto-configure a device using its MIoT spec.

    TYPE is guessed first from the spec's own service type (brand-agnostic),
    then falls back to the model-prefix table, then to 'sensor' (if no
    on/off control was found — i.e. a read-only device) or 'switch'.

    Returns None for device types in _SKIP_DEVICE_TYPES (see module docs) —
    the caller should skip creating an entity for it entirely.
    """
    conf = {}
    type_hint = None

    spec = _spec_cache_get(model)
    if spec is None:
        try:
            spec = await miio_service.miot_spec(model)
        except Exception as e:
            _LOG.warning("Spec fetch failed for %s (%s): %s", name, model, e)
            spec = None
        if isinstance(spec, dict):
            _spec_cache_put(model, spec)

    if isinstance(spec, dict) and 'services' in spec:
        device_type = _urn_name(spec.get('type', ''))
        if device_type in _SKIP_DEVICE_TYPES:
            return None
        conf, type_hint = parse_spec(spec)
        if type_hint is None:
            # Fallback for devices with no service the spec recognizes (e.g.
            # a bare-bones camera exposing only a P2P stream service) — the
            # device's own top-level type is still a useful signal.
            type_hint = _SERVICE_TYPES.get(device_type)

    conf['TYPE'] = type_hint or guess_type(model) or ('sensor' if 'ison' not in conf else 'switch')

    _LOG.info("Auto: %s (%s) → TYPE=%s ison=%s mode=%s tune=%s stat=%s extra=%d",
               name, model, conf['TYPE'], conf.get('ison'), conf.get('mode'),
               conf.get('tune'), conf.get('stat'), len(conf.get('extra_stat', [])))
    return conf


def group_members(devs):
    """Return the dids absorbed into a Mi Home device group (e.g. light groups).

    A group device (did prefixed 'group.') carries its member dids in
    extra.member_list.member_ship — including nested sub-groups. Members are
    excluded from auto-discovery so a grouped light only shows up once, as
    the group, instead of once per bulb plus the group itself.
    """
    members = set()
    for dev in devs:
        member_ship = ((dev.get('extra') or {}).get('member_list') or {}).get('member_ship') or {}
        members.update(member_ship)
    return members


async def get_home_dids(miio_service, home_name):
    """Return set of DIDs belonging to a specific home.

    - home_name is True → return None (include all, no filtering)
    - home_name is a string → return dids from that home
    - home not found or API failed → return empty set (exclude all auto devices)
    """
    if home_name is True:
        return None

    try:
        homes = await miio_service.home_list()
    except Exception as e:
        _LOG.warning("home_list unavailable: %s", e)
        return set()

    if not isinstance(homes, list):
        return set()

    for home in homes:
        if home.get('name') == home_name:
            dids = set()
            for room in home.get('roomlist', home.get('rooms', [])):
                for did in room.get('dids', room.get('device_list', [])):
                    dids.add(str(did))
            return dids

    _LOG.warning('Home "%s" not found', home_name)
    return set()
