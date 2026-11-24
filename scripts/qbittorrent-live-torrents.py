"""Builds the qBittorrent live test's content and its .torrent files, with no external tool.

Usage: qbittorrent-live-torrents.py <seed-data-dir> <out-dir>
Writes <seed-data-dir>/LivePack/{Live Show - 01.mkv, Live Show - 02.mkv, Extras/NCOP.mkv} and
<seed-data-dir>/Live Show - 03.mkv (random bytes), then pack.torrent and single.torrent in out-dir,
and prints one line per torrent: <name> <infohash>.
"""
import hashlib
import os
import sys

PIECE = 256 * 1024


def bencode(v):
    if isinstance(v, int):
        return b"i%de" % v
    if isinstance(v, bytes):
        return b"%d:%s" % (len(v), v)
    if isinstance(v, str):
        return bencode(v.encode())
    if isinstance(v, list):
        return b"l" + b"".join(bencode(x) for x in v) + b"e"
    if isinstance(v, dict):
        out = b"d"
        for k in sorted(v, key=lambda k: k.encode() if isinstance(k, str) else k):
            out += bencode(k) + bencode(v[k])
        return out + b"e"
    raise TypeError(type(v))


def pieces(paths):
    buf, out = b"", b""
    for p in paths:
        with open(p, "rb") as f:
            buf += f.read()
            while len(buf) >= PIECE:
                out += hashlib.sha1(buf[:PIECE]).digest()
                buf = buf[PIECE:]
    if buf:
        out += hashlib.sha1(buf).digest()
    return out


def write(path, size):
    os.makedirs(os.path.dirname(path), exist_ok=True)
    with open(path, "wb") as f:
        f.write(os.urandom(size))


def main(data, out):
    pack = os.path.join(data, "LivePack")
    files = [("Live Show - 01.mkv", 3 << 20), ("Live Show - 02.mkv", 3 << 20), ("Extras/NCOP.mkv", 1 << 20)]
    for rel, size in files:
        write(os.path.join(pack, rel), size)
    single = os.path.join(data, "Live Show - 03.mkv")
    write(single, 2 << 20)

    paths = [os.path.join(pack, rel) for rel, _ in files]
    pack_info = {
        "name": "LivePack",
        "piece length": PIECE,
        "pieces": pieces(paths),
        "files": [{"length": size, "path": rel.split("/")} for rel, size in files],
    }
    single_info = {"name": "Live Show - 03.mkv", "piece length": PIECE, "pieces": pieces([single]), "length": 2 << 20}
    for name, info in (("pack", pack_info), ("single", single_info)):
        with open(os.path.join(out, name + ".torrent"), "wb") as f:
            f.write(bencode({"info": info}))
        print(name, hashlib.sha1(bencode(info)).hexdigest())


if __name__ == "__main__":
    main(sys.argv[1], sys.argv[2])
