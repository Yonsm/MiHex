"""MiHex CLI — run via `python -m mihex` or the `mihex` console script."""

from asyncio import set_event_loop
from logging import (
    DEBUG,
    ERROR,
    FATAL,
    INFO,
    NOTSET,
    WARNING,
    FileHandler,
    StreamHandler,
)
from os import chdir
from os.path import abspath, dirname, exists, expanduser, join
from sys import argv

from . import __version__


def main():
    """Run MiHex service or execute a one-shot command."""
    from .entity import _LOGS, _LOOP
    from .loader import load
    from .runner import exec

    argc = len(argv)
    argi = 1
    level = -1
    if argc > 1:
        if argv[1].startswith("-h"):
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
            return
        elif argv[1].startswith("-v") or argv[1].startswith("-V"):
            argi = 2
            level = (
                [NOTSET, FATAL, ERROR, WARNING, INFO, DEBUG][min(int(argv[1][2]), 5)]
                if len(argv[1]) > 2 and argv[1][2].isdigit()
                else INFO
            )

    if argc > argi + 1 and argv[argi] == "-c":
        file = argv[argi + 1]
        argi += 2
    elif exists(file := "mihex.yaml") or exists(file := expanduser("~/mihex.yaml")):
        pass
    else:
        file = join(dirname(abspath(__file__)), "mihex.yaml")
    chdir(dirname(abspath(file)))
    _LOGS.warning("MiHex load %s", file)

    if level != NOTSET:
        _LOGS.setLevel((INFO if argc > argi else WARNING) if level == -1 else level)
        _LOGS.addHandler(
            FileHandler("/tmp/mihex.log", "w")
            if argc > 1 and argv[1].startswith("-V")
            else StreamHandler()
        )

    try:
        set_event_loop(_LOOP)
        _LOOP.run_until_complete(load(file))
        if argc > argi:
            r = _LOOP.run_until_complete(exec(argv[argi:]))
            if r is not None:
                _LOGS.warning(r)
        else:
            _LOGS.warning("\nMiHex running...")
            _LOOP.run_forever()
    except Exception as e:
        _LOGS.exception(e)
    finally:
        # _LOOP.run_until_complete(unload())
        _LOOP.close()


if __name__ == "__main__":
    main()