"""Dynamic module loader for MiHex.

Reads mihex.yaml and loads each module from the .modules package or from a
customs/ directory alongside the config file. Built-in modules (runner,
server, tracker) are loaded last to ensure all entities are registered first.
"""

from asyncio import iscoroutine
from importlib import import_module
from importlib.util import spec_from_file_location, module_from_spec
from os.path import exists, abspath, dirname, join
from sys import modules

from yaml import safe_load

from .entity import _OBJS
from logging import getLogger
_LOG = getLogger(__name__)

_BUILTINS = ("runner", "server", "tracker")


async def load(file):
    """Load the configuration file and initialize all modules."""
    with open(file) as f:
        conf = safe_load(f)

    conf_dir = dirname(abspath(file))
    for k, v in conf.items():
        if k not in _BUILTINS:
            custom = join(conf_dir, 'customs', f'{k}.py')
            mod = _import_file(k, custom) if exists(custom) else import_module("mihex.modules." + k)
            await _load(k, v, mod)

    for k in _BUILTINS:
        if k in conf:
            await _load(k, conf[k], import_module("mihex." + k))


def _import_file(name, path):
    """Load a Python file as a module and register it in sys.modules."""
    key = 'mihex.customs.' + name
    spec = spec_from_file_location(key, path)
    mod = module_from_spec(spec)
    modules[key] = mod
    spec.loader.exec_module(mod)
    return mod


async def _load(name, conf_val, mod):
    """Register a loaded module and run its setup."""
    _LOG.info("  %s", name)
    if not hasattr(mod, "NAME"):
        mod.NAME = name.upper()
    _OBJS.append(mod)
    if hasattr(mod, "setup"):
        r = mod.setup(conf_val or {})
        if iscoroutine(r):
            r = await r
        if r:
            _OBJS.extend(r if type(r) is list else [r])


# async def unload():
#     for mod in _OBJS:
#         if hasattr(mod, 'unset'):
#             r = mod.unset()
#             if iscoroutine(r):
#                 r = await r