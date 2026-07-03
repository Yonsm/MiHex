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

from ...loader import _LOGS

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
    'ptc-bath-heater': 'climate',
    'water-heater': 'climate',
    'vacuum': 'vacuum',
    'washer': 'washer',
    'curtain': 'cover',
    'window-opener': 'cover',
    'camera-control': 'camera',
    'humidifier': 'humidifier',
    'dehumidifier': 'humidifier',
    'magnet-sensor': 'binary',
    'motion-sensor': 'binary',
    'submersion-sensor': 'binary',
    'smoke-sensor': 'binary',
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
# order when picking which reading becomes the tile's headline STAT/UNIT)
_SPEC_STATS = {
    'temperature': '℃',
    'relative-humidity': '%',
    'pm2_5-density': 'aqi',
    'aqi': 'aqi',
    'co2-density': 'ppm',
    'illumination': 'lx',
    'battery-level': '%',
}

_SPEC_UNIT = {
    'celsius': '℃', 'fahrenheit': '℉',
    'percentage': '%',
    'μg/m3': 'aqi', 'μg/m³': 'aqi',
    'lux': 'lx', 'ppm': 'ppm',
}


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


def _service_type_hint(urn):
    """Guess MiHex TYPE from a service type URN's semantic name."""
    return _SERVICE_TYPES.get(_urn_name(urn))


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
    conf = {}
    stats = []  # (urn, iid, unit) candidates, in encounter order
    type_hint = None

    for svc in spec.get('services', []):
        siid = svc.get('iid', 0)
        if svc.get('description', '').lower() == 'device information':
            continue

        svc_type = _service_type_hint(svc.get('type', ''))
        if type_hint is None:
            type_hint = svc_type

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
            if svc_type == 'binary' and 'ison' not in conf \
                    and 'read' in access and prop.get('format') == 'bool':
                conf['ison'] = iid
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

    # Primary stat = highest-priority reading; the rest become extra_stat
    primary = None
    for stat_urn in _SPEC_STATS:
        primary = next((s for s in stats if s[0] == stat_urn), None)
        if primary:
            break

    if primary:
        conf['stat'] = primary[1]
        conf['UNIT'] = _SPEC_UNIT.get(primary[2].lower(), _SPEC_STATS[primary[0]])
        extras = [(iid, _SPEC_UNIT.get(unit.lower(), _SPEC_STATS[urn]))
                  for urn, iid, unit in stats if (urn, iid, unit) != primary]
        if extras:
            conf['extra_stat'] = extras

    return conf, type_hint


async def auto_config(miio_service, name, model):
    """Auto-configure a device using its MIoT spec.

    TYPE is guessed first from the spec's own service type (brand-agnostic),
    then falls back to the model-prefix table, then to 'sensor' (if no
    on/off control was found — i.e. a read-only device) or 'switch'.
    """
    conf = {}
    type_hint = None

    try:
        spec = await miio_service.miot_spec(model, 'json')
    except Exception as e:
        _LOGS.warning('MiIO spec fetch failed for %s (%s): %s', name, model, e)
        spec = None

    if isinstance(spec, dict) and 'services' in spec:
        conf, type_hint = parse_spec(spec)

    conf['TYPE'] = type_hint or guess_type(model) or ('sensor' if 'ison' not in conf else 'switch')

    _LOGS.info('MiIO auto: %s (%s) → TYPE=%s ison=%s mode=%s tune=%s stat=%s extra=%d',
               name, model, conf['TYPE'], conf.get('ison'), conf.get('mode'),
               conf.get('tune'), conf.get('stat'), len(conf.get('extra_stat', [])))
    return conf


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
        _LOGS.warning('MiIO home_list unavailable: %s', e)
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

    _LOGS.warning('MiIO home "%s" not found', home_name)
    return set()
