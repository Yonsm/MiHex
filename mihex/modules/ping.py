"""Ping module for MiHex.

Monitors device online/offline status via ICMP ping and supports
Wake-on-LAN (WoL) for remote power-on. Devices can also be shut
down via SSH.
"""

from ..entity import Entity, _LOGS
from asyncio import create_subprocess_exec, wait_for, TimeoutError as AsyncTimeout
from subprocess import PIPE


class PingEntity(Entity):
    """Entity representing a pingable device with optional WoL and SSH power-off."""

    async def update(self):
        """Check device reachability via a single ICMP ping."""
        try:
            proc = await create_subprocess_exec('ping', '-c', '1', '-W', '1', self.host, stdout=PIPE, stderr=PIPE)
            await proc.wait()
            ison = 1 if proc.returncode == 0 else None
        except Exception as e:
            _LOGS.error('Ping failed: %s %s', self.host, e)
            ison = None
        if getattr(self, 'ISON', None) != ison:
            self.ISON = ison
            return True

    async def on_turn(self, arg):
        """Toggle device power: on via WoL, off via SSH poweroff."""
        ret = await (self.on if arg else self.off)()
        return ret if ret else await super().on_turn(arg)

    async def off(self):
        """Shut down the device via SSH (poweroff command)."""
        proc = await create_subprocess_exec('ssh', self.host, 'poweroff', stdout=PIPE, stderr=PIPE)
        try:
            await wait_for(proc.wait(), timeout=5)
        except AsyncTimeout:
            proc.kill()
            return -1
        return proc.returncode

    async def on(self):
        """Power on the device via Wake-on-LAN magic packet."""
        from socket import socket, AF_INET, SOCK_DGRAM, SOL_SOCKET, SO_BROADCAST
        mac = getattr(self, 'mac', '')
        try:
            mac_bytes = bytes.fromhex(mac)
        except (ValueError, AttributeError):
            return "Invalid MAC address"
        if len(mac_bytes) != 6:
            return "Invalid MAC address"

        try:
            data = b'\xff' * 6 + mac_bytes * 16
            with socket(AF_INET, SOCK_DGRAM) as sock:
                sock.setsockopt(SOL_SOCKET, SO_BROADCAST, 1)
                sock.sendto(data, ('<broadcast>', 9))
        except Exception as e:
            return f"Exception {e}"


async def setup(conf):
    """Create PingEntity instances from the config list."""
    return [PingEntity(c, 'device') for c in conf]