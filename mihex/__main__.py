"""MiHex CLI — run via `python -m mihex` or the `mihex` console script."""

from asyncio import set_event_loop
from os import chdir
from os.path import abspath, dirname, exists, expanduser, join
from sys import argv
from logging import getLogger, DEBUG, INFO, WARNING, ERROR, CRITICAL, FileHandler, Formatter, StreamHandler, disable
_LOG = getLogger('mihex')


def usage():
    from . import __version__
    print(
        f"""
Mini Home executive {__version__}

Usage:
   %s [-v|-c conf.yaml] [ENTITY] [arg1] [arg2] [...]

   %s MQTT <Topic> [Message]
   %s DING <Message>
   %s Name [MiIO Command]

"""
        % (argv[0], argv[0], argv[0], argv[0])
    )


def main():
    """Run MiHex service or execute a one-shot command."""
    from .entity import _LOOP
    from .loader import load
    from .runner import exec

    argc = len(argv)
    argi = 1
    level = WARNING
    if argc > 1:
        if argv[1].startswith("-h"):
            usage()
            return
        elif argv[1].startswith("-v") or argv[1].startswith("-V"):
            argi = 2
            level = [CRITICAL + 1, CRITICAL, ERROR, WARNING, INFO, DEBUG][min(int(argv[1][2]), 5)] if len(argv[1]) > 2 and argv[1][2].isdigit() else INFO

    if level <= CRITICAL:
        if level != WARNING:
            _LOG.setLevel(level)
        handler = FileHandler("/tmp/mihex.log", "w") if argc > 1 and argv[1].startswith("-V") else StreamHandler()
        handler.setFormatter(Formatter("%(name)s: %(message)s"))
        _LOG.addHandler(handler)
    else:
        disable(CRITICAL + 1)

    if argc > argi + 1 and argv[argi] == "-c":
        file = argv[argi + 1]
        argi += 2
    elif exists(file := "mihex.yaml") or exists(file := expanduser("~/mihex.yaml")):
        pass
    else:
        file = join(dirname(abspath(__file__)), "mihex.yaml")
    chdir(dirname(abspath(file)))
    _LOG.warning("Load %s", file)

    try:
        set_event_loop(_LOOP)
        _LOOP.run_until_complete(load(file))
        if argc > argi:
            r = _LOOP.run_until_complete(exec(argv[argi:]))
            if r is not None:
                _LOG.warning("Result: %s", r)
        else:
            _LOG.warning("Running...")
            _LOOP.run_forever()
    except Exception as e:
        _LOG.error("Crashed: %s", e, exc_info=_LOG.isEnabledFor(DEBUG))
    finally:
        # _LOOP.run_until_complete(unload())
        _LOOP.close()


if __name__ == "__main__":
    main()