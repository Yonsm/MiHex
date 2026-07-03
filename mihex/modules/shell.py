"""Shell module for MiHex.

Controls devices via shell/SSH commands. Status is determined by
running a check command and matching its output against an expected
string. Turn on/off actions are executed as shell commands.
"""

from asyncio import create_subprocess_exec, wait_for, TimeoutError as AsyncTimeout
from subprocess import PIPE
from ..entity import Entity

_TIMEOUT = 15


def setup(conf):
    """Create ShellEntity instances from the config dictionary."""
    return [ShellEntity(c, k) for k, v in conf.items() for c in v]


class ShellEntity(Entity):
    """Entity controlled by shell commands for status check and on/off actions."""

    async def update(self):
        """Check device status by running the stat command and matching output."""
        proc = await create_subprocess_exec(*self.stat, stdout=PIPE, stderr=PIPE)
        try:
            stdout, _ = await wait_for(proc.communicate(), timeout=_TIMEOUT)
        except AsyncTimeout:
            proc.kill()
            return None
        ison = getattr(self, 'ison', None)
        stat = bool(ison and stdout.decode().startswith(ison))
        if getattr(self, 'STAT', None) != stat:
            self.STAT = stat
            return True

    async def on_turn(self, arg):
        """Execute the toon (on) or toff (off) command."""
        cmd = self.toon if arg else self.toff
        proc = await create_subprocess_exec(*cmd, stdout=PIPE, stderr=PIPE)
        try:
            await wait_for(proc.wait(), timeout=_TIMEOUT)
        except AsyncTimeout:
            proc.kill()
        await super().on_turn(arg)
