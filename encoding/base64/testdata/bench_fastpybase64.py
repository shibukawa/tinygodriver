"""Optional same-machine reference; fastpybase64 is not a project dependency.

Use the same xorshift payload and binary-byte throughput units as BenchmarkBase64.
Python result allocation and interpreter loop overhead are included.
"""

import argparse
import base64
import platform
import statistics
import time

import fastpybase64


def payload(size):
    data = bytearray(size)
    x = 123456789
    for i in range(size):
        x ^= (x << 13) & 0xFFFFFFFF
        x ^= x >> 17
        x ^= (x << 5) & 0xFFFFFFFF
        data[i] = x & 255
    return bytes(data)


def elapsed(fn, source, count):
    start = time.perf_counter()
    for _ in range(count):
        fn(source)
    return time.perf_counter() - start


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--size", type=int, default=65536)
    parser.add_argument("--samples", type=int, default=5)
    parser.add_argument("--seconds", type=float, default=0.3)
    args = parser.parse_args()
    if args.size < 1 or args.samples < 1 or args.seconds <= 0:
        parser.error("size, samples and seconds must be positive")

    raw = payload(args.size)
    plain = base64.b64encode(raw)
    mime = b"\n".join(plain[i : i + 76] for i in range(0, len(plain), 76)) + b"\n"
    print(
        f"Python {platform.python_version()}, fastpybase64 {fastpybase64.__version__}, "
        f"simdutf {fastpybase64.simdutf_version}, "
        f"kernel {fastpybase64.get_implementation()}, binary bytes {len(raw)}",
        flush=True,
    )
    for name, fn, source, expected in [
        ("Encode", fastpybase64.b64encode, raw, plain),
        ("Decode/plain", fastpybase64.b64decode, plain, raw),
        ("Decode/MIME76LF", fastpybase64.b64decode, mime, raw),
    ]:
        assert fn(source) == expected
        elapsed(fn, source, 1000)
        count = 1000
        while elapsed(fn, source, count) < args.seconds:
            count *= 2
        rates = [len(raw) * count / elapsed(fn, source, count) / 1e9 for _ in range(args.samples)]
        print(
            f"{name}: {statistics.median(rates):.3f} GB/s, "
            f"samples {[round(rate, 3) for rate in rates]}",
            flush=True,
        )


if __name__ == "__main__":
    main()
