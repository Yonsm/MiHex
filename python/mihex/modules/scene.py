"""Scene/automation engine for MiHex.

Runs simple trigger → action rules: a time (cron-style) trigger or a
device state-change trigger, firing one or more dispatch actions through
the existing runner.exec() — the same `[Entity, Method, arg...]` call
convention already used by the dashboard and CLI.

Config format:

    scene:
      - name: 温度过低自动开地暖         # optional, used only for logging
        when: {entity: 客厅温度, attr: STAT, below: 18}
        then: [[客厅地暖, on_turn, 1]]

      - name: 每天8点开新风机
        when: {at: "08:00"}            # or {at: ["08:00", "20:00"]}, or {cron: "0 8 * * *"}
        then: [[客厅新风机, on_turn, 1]]

`when` supports exactly one of:
  - `at`/`cron`                          — time trigger, checked once a minute
  - `entity`+`attr`(default STAT)+one of `below`/`above`/`equals`/`not_equals`/`changed`
                                          — state trigger, polled every _STATE_INTERVAL

State triggers are edge-triggered (fire once when the condition becomes
true, not on every poll while it stays true) and evaluated by polling
entity attributes directly rather than hooking the state-broadcast path —
simple and isolated, at the cost of up to _STATE_INTERVAL seconds of
latency, which is fine for home-automation triggers like "it's gotten
cold" rather than sub-second reactions.
"""

from asyncio import sleep, create_task
from datetime import datetime
from ..entity import _OBJS, _LOOP
from ..runner import exec as dispatch_exec
from logging import getLogger, DEBUG
_LOG = getLogger(__name__)

_CRON_INTERVAL = 60   # seconds; time rules are matched once per minute
_STATE_INTERVAL = 15  # seconds; state rules are polled this often

_time_rules = []
_state_rules = []


def setup(conf):
    """Parse scene rules from config and start the trigger loops."""
    for rule in conf or []:
        name = rule.get('name') or '?'
        when = rule.get('when') or {}
        actions = rule.get('then') or []
        if actions and isinstance(actions[0], (str, int, float)):
            actions = [actions]
        if not actions:
            _LOG.warning("Scene %r has no actions, skipping", name)
            continue

        if 'at' in when or 'cron' in when:
            for spec in _cron_specs(when):
                _time_rules.append({'name': name, 'spec': spec, 'actions': actions})
        elif 'entity' in when:
            op, value = _condition(when)
            if op is None:
                _LOG.warning("Scene %r has no usable condition, skipping", name)
                continue
            _state_rules.append({
                'name': name, 'entity': when['entity'], 'attr': when.get('attr', 'STAT'),
                'op': op, 'value': value, 'actions': actions,
                'prev_value': None, 'prev_matched': False,
            })
        else:
            _LOG.warning("Scene %r has an unrecognized trigger, skipping", name)

    if _time_rules:
        _LOG.info("  %d time rule(s)", len(_time_rules))
        _LOOP.create_task(_cron_loop())
    if _state_rules:
        _LOG.info("  %d state rule(s)", len(_state_rules))
        _LOOP.create_task(_state_loop())


def _cron_specs(when):
    """Normalize `at`/`cron` into a list of 5-field cron spec strings."""
    if 'cron' in when:
        return [when['cron']]
    at = when['at']
    specs = []
    for t in (at if isinstance(at, list) else [at]):
        hh, mm = t.split(':')
        specs.append(f'{int(mm)} {int(hh)} * * *')
    return specs


def _condition(when):
    for op in ('below', 'above', 'equals', 'not_equals'):
        if op in when:
            return op, when[op]
    if when.get('changed'):
        return 'changed', None
    return None, None


def _parse_cron_field(field, lo, hi):
    """Parse one cron field ('*', '5', '1-5', '*/15', '1-10/2', '1,3,5') into a set of ints."""
    values = set()
    for part in field.split(','):
        base, _, step_s = part.partition('/')
        step = int(step_s) if step_s else 1
        if base == '*':
            start, end = lo, hi
        elif '-' in base:
            start, end = (int(x) for x in base.split('-'))
        else:
            start = end = int(base)
        values.update(range(start, end + 1, step))
    return values


def _cron_matches(spec, now):
    fields = spec.split()
    if len(fields) != 5:
        _LOG.error("Invalid cron spec: %r", spec)
        return False
    minute, hour, day, month, weekday = fields
    return (now.minute in _parse_cron_field(minute, 0, 59)
            and now.hour in _parse_cron_field(hour, 0, 23)
            and now.day in _parse_cron_field(day, 1, 31)
            and now.month in _parse_cron_field(month, 1, 12)
            and (now.isoweekday() % 7) in _parse_cron_field(weekday, 0, 6))


async def _cron_loop():
    last_minute = None
    while True:
        now = datetime.now()
        minute_key = now.strftime('%Y-%m-%d %H:%M')
        if minute_key != last_minute:
            last_minute = minute_key
            for rule in _time_rules:
                if _cron_matches(rule['spec'], now):
                    _LOG.info("Scene fired (time): %s", rule['name'])
                    create_task(_run_actions(rule['name'], rule['actions']))
        await sleep(_CRON_INTERVAL)


def _eval_condition(op, target, value):
    try:
        if op == 'below':
            return value < target
        if op == 'above':
            return value > target
        if op == 'equals':
            return value == target
        if op == 'not_equals':
            return value != target
    except TypeError:
        _LOG.debug("Condition %s %r vs %r: incomparable types", op, value, target)
    return False


async def _state_loop():
    while True:
        for rule in _state_rules:
            ent = next((o for o in _OBJS if getattr(o, 'NAME', None) == rule['entity']), None)
            if ent is None:
                continue
            value = getattr(ent, rule['attr'], None)
            if value is None:
                continue

            if rule['op'] == 'changed':
                fired = rule['prev_value'] is not None and value != rule['prev_value']
                rule['prev_value'] = value
            else:
                matched = _eval_condition(rule['op'], rule['value'], value)
                fired = matched and not rule['prev_matched']
                rule['prev_matched'] = matched

            if fired:
                _LOG.info("Scene fired (state): %s", rule['name'])
                create_task(_run_actions(rule['name'], rule['actions']))
        await sleep(_STATE_INTERVAL)


async def _run_actions(name, actions):
    for action in actions:
        try:
            await dispatch_exec(action)
        except Exception as e:
            _LOG.error("Scene %r action failed: %s %s", name, action, e, exc_info=_LOG.isEnabledFor(DEBUG))
