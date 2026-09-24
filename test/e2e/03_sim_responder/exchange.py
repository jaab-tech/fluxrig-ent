#!/usr/bin/env python3
# Copyright (c) 2026 JAAB Tech SAS, Uruguay
# SPDX-License-Identifier: BSL-1.1
"""Send one ASCII ISO 8583 request over a socket and print the reply.

Usage: exchange.py PORT REQUEST

The frame is a 2-byte big-endian length followed by the message, which is what
io_iso8583 reads with its defaults. Exit code 2 means no reply arrived in time.
"""
import socket
import struct
import sys


def read_exactly(sock, n):
    data = b""
    while len(data) < n:
        chunk = sock.recv(n - len(data))
        if not chunk:
            raise ConnectionError("the peer closed the connection")
        data += chunk
    return data


def main():
    port = int(sys.argv[1])
    payload = sys.argv[2].encode("ascii")
    try:
        with socket.create_connection(("127.0.0.1", port), timeout=10) as sock:
            sock.sendall(struct.pack(">H", len(payload)) + payload)
            (length,) = struct.unpack(">H", read_exactly(sock, 2))
            print(read_exactly(sock, length).decode("ascii", "replace"))
    except (socket.timeout, ConnectionError) as err:
        print(f"no reply: {err}", file=sys.stderr)
        sys.exit(2)


if __name__ == "__main__":
    main()
