"""Minimal pure-Python AES-128-CBC — no third-party crypto dependency.

Implements FIPS-197 AES-128 (encrypt + decrypt) plus CBC chaining and PKCS7
padding, just enough for the MiIO local protocol's request/response
encryption (see local.py). No external dependency, matching the project's
existing "no heavy deps on resource-constrained routers" approach (see
miservice's own pure-Python RC4 for the same reasoning).

The block cipher core (encrypt_block/decrypt_block) is verified against the
official FIPS-197 Appendix B test vector and round-trips correctly; it is
not a general-purpose crypto library — no AES-192/256, no other modes.
"""

SBOX = [
    0x63, 0x7c, 0x77, 0x7b, 0xf2, 0x6b, 0x6f, 0xc5, 0x30, 0x01, 0x67, 0x2b, 0xfe, 0xd7, 0xab, 0x76,
    0xca, 0x82, 0xc9, 0x7d, 0xfa, 0x59, 0x47, 0xf0, 0xad, 0xd4, 0xa2, 0xaf, 0x9c, 0xa4, 0x72, 0xc0,
    0xb7, 0xfd, 0x93, 0x26, 0x36, 0x3f, 0xf7, 0xcc, 0x34, 0xa5, 0xe5, 0xf1, 0x71, 0xd8, 0x31, 0x15,
    0x04, 0xc7, 0x23, 0xc3, 0x18, 0x96, 0x05, 0x9a, 0x07, 0x12, 0x80, 0xe2, 0xeb, 0x27, 0xb2, 0x75,
    0x09, 0x83, 0x2c, 0x1a, 0x1b, 0x6e, 0x5a, 0xa0, 0x52, 0x3b, 0xd6, 0xb3, 0x29, 0xe3, 0x2f, 0x84,
    0x53, 0xd1, 0x00, 0xed, 0x20, 0xfc, 0xb1, 0x5b, 0x6a, 0xcb, 0xbe, 0x39, 0x4a, 0x4c, 0x58, 0xcf,
    0xd0, 0xef, 0xaa, 0xfb, 0x43, 0x4d, 0x33, 0x85, 0x45, 0xf9, 0x02, 0x7f, 0x50, 0x3c, 0x9f, 0xa8,
    0x51, 0xa3, 0x40, 0x8f, 0x92, 0x9d, 0x38, 0xf5, 0xbc, 0xb6, 0xda, 0x21, 0x10, 0xff, 0xf3, 0xd2,
    0xcd, 0x0c, 0x13, 0xec, 0x5f, 0x97, 0x44, 0x17, 0xc4, 0xa7, 0x7e, 0x3d, 0x64, 0x5d, 0x19, 0x73,
    0x60, 0x81, 0x4f, 0xdc, 0x22, 0x2a, 0x90, 0x88, 0x46, 0xee, 0xb8, 0x14, 0xde, 0x5e, 0x0b, 0xdb,
    0xe0, 0x32, 0x3a, 0x0a, 0x49, 0x06, 0x24, 0x5c, 0xc2, 0xd3, 0xac, 0x62, 0x91, 0x95, 0xe4, 0x79,
    0xe7, 0xc8, 0x37, 0x6d, 0x8d, 0xd5, 0x4e, 0xa9, 0x6c, 0x56, 0xf4, 0xea, 0x65, 0x7a, 0xae, 0x08,
    0xba, 0x78, 0x25, 0x2e, 0x1c, 0xa6, 0xb4, 0xc6, 0xe8, 0xdd, 0x74, 0x1f, 0x4b, 0xbd, 0x8b, 0x8a,
    0x70, 0x3e, 0xb5, 0x66, 0x48, 0x03, 0xf6, 0x0e, 0x61, 0x35, 0x57, 0xb9, 0x86, 0xc1, 0x1d, 0x9e,
    0xe1, 0xf8, 0x98, 0x11, 0x69, 0xd9, 0x8e, 0x94, 0x9b, 0x1e, 0x87, 0xe9, 0xce, 0x55, 0x28, 0xdf,
    0x8c, 0xa1, 0x89, 0x0d, 0xbf, 0xe6, 0x42, 0x68, 0x41, 0x99, 0x2d, 0x0f, 0xb0, 0x54, 0xbb, 0x16,
]
INV_SBOX = [0] * 256
for _i, _v in enumerate(SBOX):
    INV_SBOX[_v] = _i
RCON = [0x01, 0x02, 0x04, 0x08, 0x10, 0x20, 0x40, 0x80, 0x1B, 0x36]


def _key_expansion(key):
    """AES-128 key schedule: 16-byte key -> 44 four-byte round-key words."""
    Nk, Nr = 4, 10
    w = [list(key[4 * i:4 * i + 4]) for i in range(Nk)]
    for i in range(Nk, 4 * (Nr + 1)):
        temp = list(w[i - 1])
        if i % Nk == 0:
            temp = temp[1:] + temp[:1]  # RotWord
            temp = [SBOX[b] for b in temp]  # SubWord
            temp[0] ^= RCON[i // Nk - 1]
        w.append([w[i - Nk][j] ^ temp[j] for j in range(4)])
    return w


def _gmul(a, b):
    """Multiply two bytes in GF(2^8) with AES's reduction polynomial."""
    p = 0
    for _ in range(8):
        if b & 1:
            p ^= a
        hi = a & 0x80
        a = (a << 1) & 0xFF
        if hi:
            a ^= 0x1B
        b >>= 1
    return p


def _add_round_key(state, w, rnd):
    for c in range(4):
        word = w[4 * rnd + c]
        for r in range(4):
            state[r + 4 * c] ^= word[r]


def _sub_bytes(state):
    for i in range(16):
        state[i] = SBOX[state[i]]


def _inv_sub_bytes(state):
    for i in range(16):
        state[i] = INV_SBOX[state[i]]


def _shift_rows(state):
    new = state[:]
    for r in range(1, 4):
        for c in range(4):
            new[r + 4 * c] = state[r + 4 * ((c + r) % 4)]
    state[:] = new


def _inv_shift_rows(state):
    new = state[:]
    for r in range(1, 4):
        for c in range(4):
            new[r + 4 * ((c + r) % 4)] = state[r + 4 * c]
    state[:] = new


def _mix_columns(state):
    for c in range(4):
        s0, s1, s2, s3 = state[4 * c:4 * c + 4]
        state[4 * c + 0] = _gmul(s0, 2) ^ _gmul(s1, 3) ^ s2 ^ s3
        state[4 * c + 1] = s0 ^ _gmul(s1, 2) ^ _gmul(s2, 3) ^ s3
        state[4 * c + 2] = s0 ^ s1 ^ _gmul(s2, 2) ^ _gmul(s3, 3)
        state[4 * c + 3] = _gmul(s0, 3) ^ s1 ^ s2 ^ _gmul(s3, 2)


def _inv_mix_columns(state):
    for c in range(4):
        s0, s1, s2, s3 = state[4 * c:4 * c + 4]
        state[4 * c + 0] = _gmul(s0, 14) ^ _gmul(s1, 11) ^ _gmul(s2, 13) ^ _gmul(s3, 9)
        state[4 * c + 1] = _gmul(s0, 9) ^ _gmul(s1, 14) ^ _gmul(s2, 11) ^ _gmul(s3, 13)
        state[4 * c + 2] = _gmul(s0, 13) ^ _gmul(s1, 9) ^ _gmul(s2, 14) ^ _gmul(s3, 11)
        state[4 * c + 3] = _gmul(s0, 11) ^ _gmul(s1, 13) ^ _gmul(s2, 9) ^ _gmul(s3, 14)


def _encrypt_block(block, w):
    state = bytearray(block)
    _add_round_key(state, w, 0)
    for rnd in range(1, 10):
        _sub_bytes(state)
        _shift_rows(state)
        _mix_columns(state)
        _add_round_key(state, w, rnd)
    _sub_bytes(state)
    _shift_rows(state)
    _add_round_key(state, w, 10)
    return bytes(state)


def _decrypt_block(block, w):
    state = bytearray(block)
    _add_round_key(state, w, 10)
    for rnd in range(9, 0, -1):
        _inv_shift_rows(state)
        _inv_sub_bytes(state)
        _add_round_key(state, w, rnd)
        _inv_mix_columns(state)
    _inv_shift_rows(state)
    _inv_sub_bytes(state)
    _add_round_key(state, w, 0)
    return bytes(state)


def _pad(data, block_size=16):
    n = block_size - (len(data) % block_size)
    return data + bytes([n]) * n


def _unpad(data):
    if not data:
        raise ValueError("Cannot unpad empty data")
    n = data[-1]
    if n < 1 or n > 16 or len(data) < n:
        raise ValueError("Invalid PKCS7 padding")
    return data[:-n]


def cbc_encrypt(data, key, iv):
    """AES-128-CBC encrypt with PKCS7 padding. key/iv must each be 16 bytes."""
    w = _key_expansion(key)
    data = _pad(data)
    out = bytearray()
    prev = iv
    for i in range(0, len(data), 16):
        block = bytes(a ^ b for a, b in zip(data[i:i + 16], prev))
        enc = _encrypt_block(block, w)
        out += enc
        prev = enc
    return bytes(out)


def cbc_decrypt(data, key, iv):
    """AES-128-CBC decrypt, stripping PKCS7 padding. key/iv must each be 16 bytes."""
    if len(data) % 16:
        raise ValueError("Ciphertext is not a multiple of the block size")
    w = _key_expansion(key)
    out = bytearray()
    prev = iv
    for i in range(0, len(data), 16):
        block = data[i:i + 16]
        dec = _decrypt_block(block, w)
        out += bytes(a ^ b for a, b in zip(dec, prev))
        prev = block
    return _unpad(bytes(out))
